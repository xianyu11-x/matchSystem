use std::{io::Write, path::Path};

use tauri_plugin_dialog::DialogExt;

fn validate_file_name(file_name: &str) -> Result<(), String> {
    let path = Path::new(file_name);
    if file_name.is_empty()
        || file_name.chars().any(|character| {
            character.is_control()
                || matches!(
                    character,
                    '/' | '\\' | ':' | '*' | '?' | '"' | '<' | '>' | '|'
                )
        })
        || path.file_name().and_then(|name| name.to_str()) != Some(file_name)
    {
        return Err("建议文件名无效，请使用不含路径的文件名".into());
    }
    Ok(())
}

fn write_selected_file(path: &Path, content: &str) -> Result<String, String> {
    if !path.is_absolute() {
        return Err("保存位置不是完整路径".into());
    }
    let path_text = path.to_str().ok_or("保存路径无法转换为文本")?.to_owned();
    let parent = path.parent().ok_or("保存路径缺少目录")?;

    // 在同一目录写入临时文件，确保写入失败不会截断原文件。
    let mut temporary = tempfile::NamedTempFile::new_in(parent)
        .map_err(|error| format!("无法在所选目录创建临时文件：{error}"))?;
    temporary
        .write_all(content.as_bytes())
        .and_then(|_| temporary.as_file().sync_all())
        .map_err(|error| format!("写入 JSON 文件失败：{error}"))?;
    temporary
        .persist(path)
        .map_err(|error| format!("无法保存 JSON 文件：{}", error.error))?;
    Ok(path_text)
}

#[tauri::command]
pub async fn save_json_export(
    window: tauri::WebviewWindow,
    file_name: String,
    content: String,
) -> Result<Option<String>, String> {
    if window.label() != "main" {
        return Err("只有主窗口可以导出 JSON 文件".into());
    }
    validate_file_name(&file_name)?;

    let (sender, mut receiver) = tauri::async_runtime::channel(1);
    window
        .dialog()
        .file()
        .set_parent(&window)
        .set_title("导出 JSON 文件")
        .set_file_name(file_name)
        .add_filter("JSON 文件", &["json"])
        .save_file(move |path| {
            let _ = sender.try_send(path);
        });

    let Some(path) = receiver.recv().await.ok_or("无法打开保存对话框")? else {
        return Ok(None);
    };
    let path = path
        .into_path()
        .map_err(|error| format!("无法读取所选保存路径：{error}"))?;
    tauri::async_runtime::spawn_blocking(move || write_selected_file(&path, &content))
        .await
        .map_err(|error| format!("保存 JSON 文件的任务失败：{error}"))?
        .map(Some)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs;

    #[test]
    fn suggested_file_name_cannot_select_a_path() {
        for name in [
            "",
            ".",
            "..",
            "nested/file.json",
            "nested\\file.json",
            "C:file.json",
            "bad?.json",
        ] {
            assert!(validate_file_name(name).is_err(), "{name}");
        }
        assert!(validate_file_name("场景-1.json").is_ok());
    }

    #[test]
    fn save_writes_utf8_and_replaces_existing_contents() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("场景.json");
        let first = "{\"名称\":\"原始\"}";
        let second = "{\"名称\":\"更新\"}\n";

        assert_eq!(
            write_selected_file(&path, first).unwrap(),
            path.to_str().unwrap()
        );
        assert_eq!(fs::read(&path).unwrap(), first.as_bytes());
        assert_eq!(
            write_selected_file(&path, second).unwrap(),
            path.to_str().unwrap()
        );
        assert_eq!(fs::read(&path).unwrap(), second.as_bytes());
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 1);
    }

    #[test]
    fn failed_replacement_keeps_original_and_cleans_up_temporary_file() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("existing.json");
        fs::create_dir(&path).unwrap();
        fs::write(path.join("marker"), b"keep").unwrap();

        assert!(write_selected_file(&path, "{}")
            .unwrap_err()
            .contains("无法保存 JSON 文件"));
        assert_eq!(fs::read(path.join("marker")).unwrap(), b"keep");
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 1);
    }
}
