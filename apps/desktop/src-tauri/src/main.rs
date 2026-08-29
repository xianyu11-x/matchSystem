//! MatchScope Windows shell.
//!
//! The shell owns exactly one simulator-api sidecar.  It does not discover or
//! terminate processes by name, so an API process started outside this app is
//! never affected by a desktop shutdown.

use std::{
    io::{self, Read, Write},
    net::{TcpStream, ToSocketAddrs},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

use serde::{Deserialize, Serialize};
use tauri::{Emitter, RunEvent, Runtime, WebviewUrl, WebviewWindowBuilder, WindowEvent};
use tauri_plugin_shell::{process::CommandEvent, ShellExt};
use url::Url;

const SIDECAR_EXITED_EVENT: &str = "simulator-sidecar-exited";
const SIDECAR_ERROR_EVENT: &str = "simulator-sidecar-error";
const SIDECAR_NAME: &str = "simulator-api";
const SIDECAR_ADDR: [&str; 2] = ["--addr", "127.0.0.1:0"];

/// The only child process handle retained by the desktop app.
struct SidecarState {
    child: Mutex<Option<tauri_plugin_shell::process::CommandChild>>,
    stopping: AtomicBool,
}

impl Default for SidecarState {
    fn default() -> Self {
        Self {
            child: Mutex::new(None),
            stopping: AtomicBool::new(false),
        }
    }
}

impl SidecarState {
    fn install(&self, child: tauri_plugin_shell::process::CommandChild) {
        self.stopping.store(false, Ordering::Release);
        *self.child.lock().expect("sidecar state lock poisoned") = Some(child);
    }

    /// Kill only the handle retained for this app, once, and forget it.
    fn stop_owned(&self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(child) = self
            .child
            .lock()
            .expect("sidecar state lock poisoned")
            .take()
        {
            let _ = child.kill();
        }
    }

    fn mark_terminated(&self) {
        let _ = self
            .child
            .lock()
            .expect("sidecar state lock poisoned")
            .take();
    }

    fn is_stopping(&self) -> bool {
        self.stopping.load(Ordering::Acquire)
    }
}

#[derive(Debug, Deserialize)]
struct ReadyMessage {
    #[serde(rename = "type")]
    message_type: String,
    #[serde(rename = "apiBaseUrl")]
    api_base_url: String,
}

#[derive(Debug, Clone)]
struct ReadyInfo {
    api_base_url: String,
    endpoint: Url,
}

#[derive(Debug, Clone, Serialize)]
struct SidecarExitEvent {
    code: Option<i32>,
    signal: Option<i32>,
    expected: bool,
}

#[derive(Debug, Clone, Serialize)]
struct SidecarErrorEvent {
    message: String,
}

fn startup_error(message: impl Into<String>) -> Box<dyn std::error::Error> {
    Box::new(io::Error::new(io::ErrorKind::Other, message.into()))
}

fn parse_ready_line(bytes: &[u8]) -> Result<ReadyInfo, String> {
    let line = std::str::from_utf8(bytes)
        .map_err(|error| format!("sidecar readiness is not UTF-8: {error}"))?
        .trim();
    let message: ReadyMessage = serde_json::from_str(line)
        .map_err(|error| format!("invalid sidecar readiness JSON: {error}"))?;
    if message.message_type != "ready" {
        return Err(format!(
            "unexpected sidecar readiness type {:?}",
            message.message_type
        ));
    }

    let endpoint = validate_api_base_url(&message.api_base_url)?;
    Ok(ReadyInfo {
        api_base_url: message.api_base_url,
        endpoint,
    })
}

fn validate_api_base_url(raw: &str) -> Result<Url, String> {
    let endpoint = Url::parse(raw).map_err(|error| format!("invalid sidecar API URL: {error}"))?;
    if endpoint.scheme() != "http" {
        return Err("sidecar API URL must use http".to_string());
    }
    if endpoint.username() != ""
        || endpoint.password().is_some()
        || endpoint.query().is_some()
        || endpoint.fragment().is_some()
    {
        return Err("sidecar API URL must not contain credentials, query, or fragment".to_string());
    }
    if !matches!(endpoint.host_str(), Some("127.0.0.1" | "localhost" | "::1")) {
        return Err("sidecar API URL must point to the local loopback host".to_string());
    }
    if endpoint.port().is_none() {
        return Err("sidecar API URL must include a dynamically assigned port".to_string());
    }
    if !endpoint.path().is_empty() && endpoint.path() != "/" {
        return Err("sidecar API URL must not contain a path".to_string());
    }
    Ok(endpoint)
}

/// The Go readiness contract is an origin; the Web API contract is rooted at
/// /api/v1. Keep that translation in the shell so the Web build remains
/// transport-agnostic.
fn web_api_base_url(origin: &str) -> String {
    format!("{}/api/v1", origin.trim_end_matches('/'))
}

/// Probe the API without adding a second HTTP client dependency to the shell.
fn probe_health(endpoint: &Url) -> io::Result<bool> {
    let host = endpoint
        .host_str()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "missing health host"))?;
    let port = endpoint
        .port()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "missing health port"))?;
    let socket = if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    };
    let address = socket
        .to_socket_addrs()?
        .next()
        .ok_or_else(|| io::Error::new(io::ErrorKind::AddrNotAvailable, "health address empty"))?;
    let mut stream = TcpStream::connect_timeout(&address, Duration::from_millis(250))?;
    stream.set_read_timeout(Some(Duration::from_millis(500)))?;
    stream.set_write_timeout(Some(Duration::from_millis(500)))?;
    let host_header = if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    };
    stream.write_all(
        format!("GET /api/v1/health HTTP/1.1\r\nHost: {host_header}\r\nConnection: close\r\n\r\n")
            .as_bytes(),
    )?;

    let mut response = [0_u8; 512];
    let count = stream.read(&mut response)?;
    if count == 0 {
        return Ok(false);
    }
    let status_line = String::from_utf8_lossy(&response[..count]);
    Ok(status_line
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        == Some("200"))
}

fn wait_for_health(endpoint: &Url) -> Result<(), String> {
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        let last_error = match probe_health(endpoint) {
            Ok(true) => return Ok(()),
            Ok(false) => "health endpoint returned a non-200 response".to_string(),
            Err(error) => error.to_string(),
        };
        if Instant::now() >= deadline {
            return Err(format!("{last_error} within 15 seconds"));
        }
        thread::sleep(Duration::from_millis(50));
    }
}

fn start_sidecar<R: Runtime>(
    app: &mut tauri::App<R>,
    state: Arc<SidecarState>,
) -> Result<String, Box<dyn std::error::Error>> {
    let command = app
        .handle()
        .shell()
        .sidecar(SIDECAR_NAME)
        .map_err(|error| startup_error(format!("cannot resolve simulator-api sidecar: {error}")))?
        .args(SIDECAR_ADDR);
    let (mut events, child) = command
        .spawn()
        .map_err(|error| startup_error(format!("cannot start simulator-api sidecar: {error}")))?;
    state.install(child);

    let (ready_sender, mut ready_receiver) =
        tauri::async_runtime::channel::<Result<ReadyInfo, String>>(1);
    let monitor_state = state.clone();
    let monitor_app = app.handle().clone();
    tauri::async_runtime::spawn(async move {
        let mut ready_sender = Some(ready_sender);
        while let Some(event) = events.recv().await {
            match event {
                CommandEvent::Stdout(line) => {
                    if line.iter().all(|byte| byte.is_ascii_whitespace()) {
                        continue;
                    }
                    if let Some(sender) = ready_sender.take() {
                        let result = parse_ready_line(&line);
                        let failed = result.is_err();
                        let _ = sender.send(result).await;
                        if failed {
                            break;
                        }
                    } else {
                        // stdout is reserved for the one-line readiness protocol.
                        eprintln!("simulator-api: unexpected stdout after readiness");
                    }
                }
                CommandEvent::Stderr(line) => {
                    eprintln!("simulator-api: {}", String::from_utf8_lossy(&line));
                }
                CommandEvent::Error(message) => {
                    if let Some(sender) = ready_sender.take() {
                        let _ = sender.send(Err(message)).await;
                        break;
                    }
                    let _ = monitor_app.emit(SIDECAR_ERROR_EVENT, SidecarErrorEvent { message });
                }
                CommandEvent::Terminated(payload) => {
                    let expected = monitor_state.is_stopping();
                    monitor_state.mark_terminated();
                    if let Some(sender) = ready_sender.take() {
                        let _ = sender
                            .send(Err(format!(
                                "simulator-api exited before readiness (code {:?})",
                                payload.code
                            )))
                            .await;
                    } else if !expected {
                        let _ = monitor_app.emit(
                            SIDECAR_EXITED_EVENT,
                            SidecarExitEvent {
                                code: payload.code,
                                signal: payload.signal,
                                expected: false,
                            },
                        );
                    }
                    break;
                }
                // Keep the monitor forward-compatible with new shell events.
                _ => {}
            }
        }

        if let Some(sender) = ready_sender.take() {
            let _ = sender
                .send(Err(
                    "simulator-api output stream closed before readiness".to_string()
                ))
                .await;
        }
    });

    let ready = tauri::async_runtime::block_on(async { ready_receiver.recv().await });
    let ready = match ready {
        Some(Ok(ready)) => ready,
        Some(Err(error)) => {
            state.stop_owned();
            return Err(startup_error(error));
        }
        None => {
            state.stop_owned();
            return Err(startup_error("sidecar readiness channel closed"));
        }
    };

    if let Err(error) = wait_for_health(&ready.endpoint) {
        state.stop_owned();
        return Err(startup_error(error));
    }
    Ok(ready.api_base_url)
}

fn create_main_window<R: Runtime>(
    app: &mut tauri::App<R>,
    state: Arc<SidecarState>,
    api_origin: String,
) -> Result<(), Box<dyn std::error::Error>> {
    // JSON encoding makes the value safe to embed in a document-start script.
    let api_base_url = web_api_base_url(&api_origin);
    let api_literal = serde_json::to_string(&api_base_url)
        .map_err(|error| startup_error(format!("cannot encode API base URL: {error}")))?;
    let initialization_script =
        format!("window.__MATCH_API_BASE_URL__ = {api_literal}; window.__MATCH_DESKTOP__ = true;");

    #[cfg(debug_assertions)]
    let webview_url = WebviewUrl::External(
        "http://127.0.0.1:5173"
            .parse::<Url>()
            .map_err(|error| startup_error(format!("invalid Vite dev URL: {error}")))?,
    );
    #[cfg(not(debug_assertions))]
    let webview_url = WebviewUrl::App("index.html".into());

    let window = WebviewWindowBuilder::new(app, "main", webview_url)
        .title("MatchScope")
        .inner_size(1440.0, 900.0)
        .min_inner_size(1024.0, 640.0)
        .initialization_script(initialization_script)
        .build()
        .map_err(|error| startup_error(format!("cannot create main window: {error}")))?;
    let app_handle = app.handle().clone();
    window.on_window_event(move |event| {
        if matches!(event, WindowEvent::CloseRequested { .. }) {
            state.stop_owned();
            app_handle.exit(0);
        }
    });
    Ok(())
}

fn main() {
    let state = Arc::new(SidecarState::default());
    let setup_state = state.clone();
    let run_state = state.clone();

    let app = tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(state)
        .setup(move |app| {
            let api_origin = start_sidecar(app, setup_state.clone())?;
            if let Err(error) = create_main_window(app, setup_state.clone(), api_origin) {
                setup_state.stop_owned();
                return Err(error);
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building MatchScope desktop application");

    app.run(move |_app_handle, event| {
        if matches!(event, RunEvent::ExitRequested { .. } | RunEvent::Exit) {
            run_state.stop_owned();
        }
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn web_base_url_adds_api_prefix_to_ready_origin() {
        assert_eq!(
            web_api_base_url("http://127.0.0.1:43127"),
            "http://127.0.0.1:43127/api/v1"
        );
        assert_eq!(
            web_api_base_url("http://127.0.0.1:43127/"),
            "http://127.0.0.1:43127/api/v1"
        );
    }

    #[test]
    fn ready_contract_is_loopback_origin() {
        let ready = parse_ready_line(br#"{"type":"ready","apiBaseUrl":"http://127.0.0.1:43127"}"#)
            .expect("valid ready line");
        assert_eq!(ready.api_base_url, "http://127.0.0.1:43127");
        assert!(validate_api_base_url("https://127.0.0.1:43127").is_err());
        assert!(validate_api_base_url("http://example.test:43127").is_err());
    }
}
