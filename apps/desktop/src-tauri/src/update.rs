//! The desktop stages updates; the native helper owns replacement after exit.
use serde_json::Value;
use std::{
    fs,
    process::Command,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    time::{Duration, Instant},
};

#[path = "update_download.rs"]
mod download;
static INSTALLING: AtomicBool = AtomicBool::new(false);

#[tauri::command]
pub async fn check_desktop_update() -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(|| {
        serde_json::to_value(download::check()?).map_err(|e| e.to_string())
    })
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
        let prepared = download::prepare(&version)?;
        let mut command = Command::new(&prepared.helper);
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x08000000); // CREATE_NO_WINDOW, no shell interpreter.
        }
        let mut helper = command
            .current_dir(prepared.context.parent().ok_or("更新事务目录缺失")?)
            .arg("--context")
            .arg(&prepared.context)
            .spawn()
            .map_err(|e| format!("无法启动 Updater.exe：{e}"))?;
        let deadline = Instant::now() + Duration::from_secs(15);
        while !prepared.armed.exists() {
            if helper.try_wait().map_err(|e| e.to_string())?.is_some() {
                return Err(format!(
                    "更新进程启动失败；当前客户端仍在运行。详情：{}",
                    prepared
                        .context
                        .parent()
                        .unwrap()
                        .join("recovery-error.json")
                        .display()
                ));
            }
            if Instant::now() >= deadline {
                let _ = helper.kill();
                let _ = helper.wait();
                return Err("更新进程启动超时；当前客户端仍在运行".into());
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
