//! Windows portable updates are staged before the shell relinquishes its sidecar.
use serde_json::Value;
use std::{
    fs,
    path::PathBuf,
    process::Command,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::{Duration, Instant},
};

static INSTALLING: AtomicBool = AtomicBool::new(false);
const SCRIPT: &str = include_str!("update.ps1");

fn script_path() -> Result<PathBuf, String> {
    let directory = std::env::temp_dir().join(format!("matchscope-updater-{}", std::process::id()));
    fs::create_dir_all(&directory).map_err(|e| e.to_string())?;
    let path = directory.join("update.ps1");
    fs::write(&path, SCRIPT).map_err(|e| e.to_string())?;
    Ok(path)
}
fn powershell() -> Command {
    let mut command = Command::new("powershell.exe");
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        command.creation_flags(0x08000000); // CREATE_NO_WINDOW
    }
    command.args([
        "-NoProfile",
        "-NonInteractive",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
    ]);
    command
}
fn run(mode: &str, expected: &str) -> Result<Value, String> {
    let executable = std::env::current_exe().map_err(|e| e.to_string())?;
    let architecture = match std::env::consts::ARCH {
        "x86_64" => "x64",
        "aarch64" => "arm64",
        _ => return Err("不支持当前 CPU 架构".into()),
    };
    let output = powershell()
        .arg(script_path()?)
        .args([
            "-Mode",
            mode,
            "-Repository",
            env!("MATCHSCOPE_UPDATE_REPOSITORY"),
            "-CurrentVersion",
            env!("CARGO_PKG_VERSION"),
            "-Architecture",
            architecture,
            "-ExpectedVersion",
            expected,
            "-OwnerPid",
            &std::process::id().to_string(),
            "-InstallDirectory",
        ])
        .arg(executable.parent().ok_or("无法定位安装目录")?)
        .output()
        .map_err(|e| format!("无法运行 Windows PowerShell：{e}"))?;
    if !output.status.success() {
        return Err(String::from_utf8_lossy(&output.stderr).trim().to_string());
    }
    serde_json::from_slice(&output.stdout).map_err(|e| format!("更新器返回无效结果：{e}"))
}
#[tauri::command]
pub async fn check_desktop_update() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| run("Check", "none"))
        .await
        .map_err(|e| e.to_string())?
}
#[tauri::command]
pub async fn install_desktop_update(
    app: tauri::AppHandle,
    state: tauri::State<'_, Arc<crate::SidecarState>>,
    version: String,
) -> Result<(), String> {
    if INSTALLING.swap(true, Ordering::AcqRel) {
        return Err("更新已在进行中".into());
    }
    let result = tauri::async_runtime::spawn_blocking(move || -> Result<(), String> {
        let prepared = run("Prepare", &version)?;
        let context = prepared["contextPath"].as_str().ok_or("更新事务路径缺失")?;
        let armed = prepared["armed"].as_str().ok_or("更新就绪路径缺失")?;
        let mut helper = powershell()
            .arg(script_path()?)
            .current_dir(
                std::path::Path::new(context)
                    .parent()
                    .ok_or("更新事务目录缺失")?,
            )
            .args(["-Mode", "Apply", "-ContextPath", context])
            .spawn()
            .map_err(|e| e.to_string())?;
        let deadline = Instant::now() + Duration::from_secs(15);
        while !std::path::Path::new(armed).exists() {
            if helper.try_wait().map_err(|e| e.to_string())?.is_some() {
                return Err("更新进程启动失败；当前客户端仍在运行".into());
            }
            if Instant::now() >= deadline {
                let _ = helper.kill();
                return Err("更新进程启动超时".into());
            }
            std::thread::sleep(Duration::from_millis(100));
        }
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())
    .and_then(|r| r);
    if result.is_ok() {
        state.stop_owned();
        app.exit(0);
    } else {
        INSTALLING.store(false, Ordering::Release);
    }
    result
}
#[tauri::command]
pub fn desktop_update_status() -> Option<Value> {
    let path = std::env::current_exe()
        .ok()?
        .parent()?
        .join(".matchscope-update-status.json");
    serde_json::from_slice(&fs::read(path).ok()?).ok()
}
pub fn acknowledge_startup() -> std::io::Result<()> {
    if let Some(path) = std::env::var_os("MATCHSCOPE_UPDATE_ACK") {
        fs::write(path, b"healthy")?;
        std::env::remove_var("MATCHSCOPE_UPDATE_ACK");
    }
    Ok(())
}
