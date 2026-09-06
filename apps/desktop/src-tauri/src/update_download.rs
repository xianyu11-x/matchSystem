//! Release discovery and staging stay in the desktop process, before it exits.
use matchscope_updater::Transaction;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::HashSet,
    fs::{self, File, OpenOptions},
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
    time::Duration,
};
use url::Url;

type Result<T> = std::result::Result<T, String>;
const MAX_ZIP: u64 = 1024 * 1024 * 1024;
const MAX_EXPANDED: u64 = 2 * MAX_ZIP;
const REQUIRED: [&str; 3] = ["MatchScope.exe", "simulator-api.exe", "Updater.exe"];

#[derive(Clone, Deserialize)]
struct Asset {
    name: String,
    state: String,
    browser_download_url: String,
    size: u64,
    digest: Option<String>,
}
#[derive(Deserialize)]
struct GithubRelease {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    body: Option<String>,
    assets: Vec<Asset>,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Release {
    current_version: String,
    version: String,
    available: bool,
    repository: String,
    notes: String,
    asset_name: Option<String>,
    #[serde(skip)]
    asset: Option<Asset>,
    #[serde(skip)]
    assets: Vec<Asset>,
}

fn stable_version(tag: &str) -> Result<[u64; 3]> {
    let parts: Vec<_> = tag.strip_prefix('v').unwrap_or(tag).split('.').collect();
    if parts.len() != 3 {
        return Err("Release 标签必须为稳定的 vMAJOR.MINOR.PATCH".into());
    }
    let mut version = [0; 3];
    for (index, part) in parts.iter().enumerate() {
        if part.is_empty()
            || !part.bytes().all(|c| c.is_ascii_digit())
            || (part.len() > 1 && part.starts_with('0'))
        {
            return Err("Release 标签必须为稳定的 vMAJOR.MINOR.PATCH".into());
        }
        version[index] = part.parse().map_err(|_| "Release 版本号超出范围")?;
    }
    Ok(version)
}
fn architecture() -> Result<&'static str> {
    match std::env::consts::ARCH {
        "x86_64" => Ok("x64"),
        "aarch64" => Ok("arm64"),
        _ => Err("不支持当前 CPU 架构".into()),
    }
}
fn client() -> Result<Client> {
    Client::builder()
        .user_agent("MatchScope-Updater")
        .https_only(true)
        .connect_timeout(Duration::from_secs(30))
        .timeout(Duration::from_secs(30))
        .redirect(reqwest::redirect::Policy::limited(10))
        .build()
        .map_err(|e| e.to_string())
}
fn limited_bytes(mut reader: impl Read, limit: u64) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    reader
        .by_ref()
        .take(limit + 1)
        .read_to_end(&mut bytes)
        .map_err(|e| e.to_string())?;
    if bytes.len() as u64 > limit {
        return Err("远端响应超过大小限制".into());
    }
    Ok(bytes)
}
fn select_release(
    release: GithubRelease,
    repository: &str,
    current: &str,
    arch: &str,
) -> Result<Release> {
    if release.draft || release.prerelease {
        return Err("仅支持已发布的稳定版本".into());
    }
    let latest = stable_version(&release.tag_name)?;
    let version = format!("{}.{}.{}", latest[0], latest[1], latest[2]);
    let mut asset = None;
    for name in [
        format!("MatchScope-{version}-windows-{arch}-portable.zip"),
        format!("MatchScope-{version}-windows-{arch}.zip"),
    ] {
        let matches: Vec<_> = release
            .assets
            .iter()
            .filter(|a| a.name == name && a.state == "uploaded")
            .collect();
        if matches.len() == 1 {
            asset = Some(matches[0].clone());
            break;
        }
    }
    Ok(Release {
        available: latest > stable_version(current)?,
        current_version: current.into(),
        version,
        repository: repository.into(),
        notes: release.body.unwrap_or_default(),
        asset_name: asset.as_ref().map(|a| a.name.clone()),
        asset,
        assets: release.assets,
    })
}
fn get_release(client: &Client) -> Result<Release> {
    let repository = env!("MATCHSCOPE_UPDATE_REPOSITORY");
    let response = client
        .get(format!(
            "https://api.github.com/repos/{repository}/releases/latest"
        ))
        .header("Accept", "application/vnd.github+json")
        .send()
        .and_then(|r| r.error_for_status())
        .map_err(|e| format!("检查更新失败：{e}"))?;
    let release = serde_json::from_slice(&limited_bytes(response, 4 * 1024 * 1024)?)
        .map_err(|e| format!("Release 数据无效：{e}"))?;
    select_release(
        release,
        repository,
        env!("CARGO_PKG_VERSION"),
        architecture()?,
    )
}
pub fn check() -> Result<Release> {
    get_release(&client()?)
}
fn download_url(raw: &str, repository: &str) -> Result<Url> {
    let url = Url::parse(raw).map_err(|e| e.to_string())?;
    if url.scheme() != "https"
        || url.host_str() != Some("github.com")
        || !url
            .path()
            .starts_with(&format!("/{repository}/releases/download/"))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
    {
        return Err("更新下载地址不属于指定的 GitHub 仓库".into());
    }
    Ok(url)
}
fn valid_hash(hash: &str) -> bool {
    hash.len() == 64 && hash.bytes().all(|c| c.is_ascii_hexdigit())
}
fn checksum_line(body: &str, name: &str) -> Option<String> {
    body.lines().find_map(|line| {
        let line = line.trim();
        let split = line.find(char::is_whitespace)?;
        let hash = &line[..split];
        let file = line[split..]
            .trim_start()
            .strip_prefix('*')
            .unwrap_or(line[split..].trim_start());
        (valid_hash(hash) && file == name).then(|| hash.to_ascii_lowercase())
    })
}
fn expected_hash(client: &Client, release: &Release, asset: &Asset) -> Result<String> {
    if let Some(hash) = asset
        .digest
        .as_deref()
        .and_then(|v| v.strip_prefix("sha256:"))
    {
        if valid_hash(hash) {
            return Ok(hash.to_ascii_lowercase());
        }
    }
    for name in [format!("{}.sha256", asset.name), "SHA256SUMS.txt".into()] {
        if let Some(checksum) = release
            .assets
            .iter()
            .find(|a| a.name == name && a.state == "uploaded")
        {
            let response = client
                .get(download_url(
                    &checksum.browser_download_url,
                    &release.repository,
                )?)
                .send()
                .and_then(|r| r.error_for_status())
                .map_err(|e| e.to_string())?;
            let body = String::from_utf8(limited_bytes(response, 1024 * 1024)?)
                .map_err(|e| e.to_string())?;
            if let Some(hash) = checksum_line(&body, &asset.name) {
                return Ok(hash);
            }
        }
    }
    Err("Release 必须提供 GitHub SHA-256 digest 或含对应 ZIP 的校验和附件".into())
}
fn download_zip(mut response: impl Read, path: &Path, size: u64, expected: &str) -> Result<()> {
    if size == 0 || size > MAX_ZIP {
        return Err("ZIP 大小无效，上限为 1 GiB".into());
    }
    let mut output = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
        .map_err(|e| e.to_string())?;
    let mut hash = Sha256::new();
    let mut total = 0u64;
    let mut buffer = [0u8; 64 * 1024];
    loop {
        let read = response.read(&mut buffer).map_err(|e| e.to_string())?;
        if read == 0 {
            break;
        }
        total += read as u64;
        if total > size {
            return Err("ZIP 下载超过发布资产大小".into());
        }
        hash.update(&buffer[..read]);
        output
            .write_all(&buffer[..read])
            .map_err(|e| e.to_string())?;
    }
    if total != size {
        return Err("ZIP 下载不完整".into());
    }
    if format!("{:x}", hash.finalize()) != expected.to_ascii_lowercase() {
        return Err("ZIP SHA-256 校验失败".into());
    }
    output.sync_all().map_err(|e| e.to_string())
}
fn safe_entry(name: &str) -> bool {
    !name.is_empty()
        && !name.starts_with('/')
        && name.trim_end_matches('/').split('/').all(|part| {
            let stem = part.split('.').next().unwrap_or("").to_ascii_uppercase();
            let device = matches!(
                stem.as_str(),
                "CON" | "PRN" | "AUX" | "NUL" | "CONIN$" | "CONOUT$"
            ) || ((stem.starts_with("COM") || stem.starts_with("LPT"))
                && stem.len() == 4
                && stem.as_bytes()[3].is_ascii_digit());
            !part.is_empty()
                && part != "."
                && part != ".."
                && !part.ends_with([' ', '.'])
                && !part
                    .chars()
                    .any(|c| c.is_control() || "<>:\"|?*".contains(c))
                && !device
        })
}
fn assert_executable(path: &Path) -> Result<()> {
    no_reparse_ancestors(path)?;
    if !fs::symlink_metadata(path).map_err(|e| e.to_string())?.is_file() {
        return Err(format!("EXE 必须是普通文件：{}", path.display()));
    }
    let mut file = File::open(path).map_err(|e| format!("缺失 {}：{e}", path.display()))?;
    let mut header = [0u8; 64];
    file.read_exact(&mut header)
        .map_err(|_| format!("无效 EXE：{}", path.display()))?;
    if &header[..2] != b"MZ" {
        return Err(format!("无效 EXE：{}", path.display()));
    }
    let offset = u32::from_le_bytes(header[60..64].try_into().unwrap());
    file.seek(SeekFrom::Start(offset as u64))
        .map_err(|e| e.to_string())?;
    let mut pe = [0u8; 6];
    file.read_exact(&mut pe).map_err(|e| e.to_string())?;
    let machine = match architecture()? {
        "x64" => 0x8664u16,
        _ => 0xaa64u16,
    };
    if &pe[..4] != b"PE\0\0" || u16::from_le_bytes([pe[4], pe[5]]) != machine {
        return Err(format!("EXE 架构与客户端不匹配：{}", path.display()));
    }
    Ok(())
}
fn expand_portable(zip: &Path, destination: &Path) -> Result<()> {
    let mut archive = zip::ZipArchive::new(File::open(zip).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    if archive.len() > 4096 {
        return Err("ZIP 文件数超过 4096".into());
    }
    // zip indexes entries by name, so exact duplicates disappear from len()/by_index().
    // Count the original central records to reject those archives instead of silently choosing one.
    let mut central = File::open(zip).map_err(|e| e.to_string())?;
    central
        .seek(SeekFrom::Start(archive.central_directory_start()))
        .map_err(|e| e.to_string())?;
    let mut count = 0;
    loop {
        let mut signature = [0; 4];
        central
            .read_exact(&mut signature)
            .map_err(|e| e.to_string())?;
        if signature != *b"PK\x01\x02" {
            break;
        }
        let mut header = [0; 42];
        central.read_exact(&mut header).map_err(|e| e.to_string())?;
        let extra = [24, 26, 28]
            .iter()
            .map(|i| u16::from_le_bytes([header[*i], header[*i + 1]]) as i64)
            .sum();
        central
            .seek(SeekFrom::Current(extra))
            .map_err(|e| e.to_string())?;
        count += 1;
        if count > 4096 {
            return Err("ZIP 文件数超过 4096".into());
        }
    }
    if count != archive.len() {
        return Err("ZIP 包含重复的中央目录路径".into());
    }
    let prefix = if archive
        .file_names()
        .any(|n| n.replace('\\', "/") == "portable/MatchScope.exe")
    {
        "portable/"
    } else {
        ""
    };
    let mut seen = HashSet::new();
    let mut total = 0u64;
    for index in 0..archive.len() {
        let mut entry = archive.by_index(index).map_err(|e| e.to_string())?;
        let name = entry.name().replace('\\', "/");
        let kind = entry.unix_mode().unwrap_or(0) & 0o170000;
        if !safe_entry(&name) || !matches!(kind, 0 | 0o100000 | 0o040000) {
            return Err(format!("不安全的 ZIP 路径：{name}"));
        }
        let Some(relative) = name.strip_prefix(prefix) else {
            continue;
        };
        if relative.is_empty() || relative.ends_with('/') {
            continue;
        }
        if !seen.insert(relative.to_lowercase()) {
            return Err("ZIP 包含重复路径".into());
        }
        total = total.checked_add(entry.size()).ok_or("ZIP 大小溢出")?;
        if total > MAX_EXPANDED {
            return Err("ZIP 解压超过 2 GiB".into());
        }
        let target = destination.join(relative);
        fs::create_dir_all(target.parent().ok_or("ZIP 路径缺少父目录")?)
            .map_err(|e| e.to_string())?;
        let mut output = OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&target)
            .map_err(|e| e.to_string())?;
        let size = entry.size();
        let written = std::io::copy(&mut (&mut entry).take(size + 1), &mut output)
            .map_err(|e| e.to_string())?;
        if written != size {
            return Err("ZIP 解压大小与条目声明不符".into());
        }
        output.sync_all().map_err(|e| e.to_string())?;
    }
    for name in REQUIRED {
        assert_executable(&destination.join(name))?;
    }
    Ok(())
}
fn no_reparse_ancestors(path: &Path) -> Result<()> {
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        for ancestor in path.ancestors() {
            let metadata = fs::symlink_metadata(ancestor).map_err(|e| e.to_string())?;
            if metadata.file_attributes() & 0x400 != 0 {
                return Err("更新路径不能包含链接或 junction".into());
            }
        }
    }
    Ok(())
}
pub struct Prepared {
    pub context: PathBuf,
    pub helper: PathBuf,
    pub armed: PathBuf,
}
pub fn prepare(expected_version: &str) -> Result<Prepared> {
    let executable = std::env::current_exe().map_err(|e| e.to_string())?;
    if executable.file_name().and_then(|n| n.to_str()) != Some("MatchScope.exe") {
        return Err("自动更新仅支持独立便携目录中的 MatchScope.exe".into());
    }
    let install = executable.parent().ok_or("无法定位安装目录")?.to_path_buf();
    no_reparse_ancestors(&install)?;
    assert_executable(&install.join("Updater.exe"))?;
    let parent = install
        .parent()
        .ok_or("安装目录必须有可写父目录")?
        .to_path_buf();
    let client = client()?;
    let release = get_release(&client)?;
    if !release.available || release.version != expected_version {
        return Err("Release 已变化或不再有新版，请重新检查更新".into());
    }
    let asset = release
        .asset
        .as_ref()
        .ok_or("Release 没有匹配当前架构的 ZIP")?;
    if asset.size == 0 || asset.size > MAX_ZIP {
        return Err("ZIP 大小无效，上限为 1 GiB".into());
    }
    let url = download_url(&asset.browser_download_url, &release.repository)?;
    let hash = expected_hash(&client, &release, asset)?;
    let name = format!(".matchscope-update-{}", uuid::Uuid::new_v4().simple());
    let work = parent.join(&name);
    fs::create_dir(&work).map_err(|e| format!("无法创建更新目录：{e}"))?;
    let zip = work.join("release.zip");
    let response = client
        .get(url)
        .timeout(Duration::from_secs(600))
        .send()
        .and_then(|r| r.error_for_status())
        .map_err(|e| format!("下载更新失败：{e}"))?;
    download_zip(response, &zip, asset.size, &hash)?;
    let stage = parent.join(format!("{name}-stage"));
    fs::create_dir(&stage).map_err(|e| e.to_string())?;
    expand_portable(&zip, &stage)?;
    // Execute the CURRENT trusted helper outside the installation, never the downloaded helper.
    let helper = work.join("Updater.exe");
    fs::copy(install.join("Updater.exe"), &helper).map_err(|e| e.to_string())?;
    let transaction = Transaction {
        protocol_version: 1,
        install,
        stage,
        backup: parent.join(format!("{name}-backup")),
        failed: parent.join(format!("{name}-failed")),
        pid: std::process::id(),
        ack: work.join("healthy"),
        armed: work.join("armed"),
    };
    let context = work.join("transaction.json");
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&context)
        .map_err(|e| e.to_string())?;
    serde_json::to_writer_pretty(&mut file, &transaction).map_err(|e| e.to_string())?;
    file.sync_all().map_err(|e| e.to_string())?;
    Ok(Prepared {
        context,
        helper,
        armed: transaction.armed,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;
    struct Directory(PathBuf);
    impl Directory {
        fn new() -> Self {
            let path = std::env::temp_dir()
                .join(format!("matchscope-download-test-{}", uuid::Uuid::new_v4()));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }
    impl Drop for Directory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }
    fn executable() -> Vec<u8> {
        let mut bytes = vec![0; 70];
        bytes[..2].copy_from_slice(b"MZ");
        bytes[60..64].copy_from_slice(&64u32.to_le_bytes());
        bytes[64..68].copy_from_slice(b"PE\0\0");
        bytes[68..70].copy_from_slice(
            &(if architecture().unwrap() == "x64" {
                0x8664u16
            } else {
                0xaa64u16
            })
            .to_le_bytes(),
        );
        bytes
    }
    fn archive(names: &[&str], root: &Path) -> PathBuf {
        let path = root.join("release.zip");
        let mut writer = zip::ZipWriter::new(File::create(&path).unwrap());
        for name in names {
            writer
                .start_file(*name, zip::write::SimpleFileOptions::default())
                .unwrap();
            writer.write_all(&executable()).unwrap();
        }
        writer.finish().unwrap();
        path
    }
    #[test]
    fn stable_versions_are_numeric_and_reject_prereleases() {
        assert!(stable_version("v0.1.10").unwrap() > stable_version("0.1.9").unwrap());
        for bad in [
            "1.0",
            "1.0.0-beta.1",
            "1.0.0+build",
            "01.0.0",
            "1.-1.0",
            "V1.0.0",
        ] {
            assert!(stable_version(bad).is_err(), "{bad}");
        }
    }
    #[test]
    fn release_prefers_portable_and_matches_architecture() {
        let release: GithubRelease = serde_json::from_value(serde_json::json!({
            "tag_name": "v0.1.10", "draft": false, "prerelease": false, "body": null,
            "assets": (["MatchScope-0.1.10-windows-x64.zip", "MatchScope-0.1.10-windows-x64-portable.zip", "MatchScope-0.1.10-windows-arm64-portable.zip"].map(|name| serde_json::json!({"name":name,"state":"uploaded","browser_download_url":"","size":10})))
        })).unwrap();
        let selected = select_release(release, "owner/repo", "0.1.9", "x64").unwrap();
        assert!(selected.available);
        assert_eq!(
            selected.asset_name.as_deref(),
            Some("MatchScope-0.1.10-windows-x64-portable.zip")
        );
    }
    #[test]
    fn urls_stay_in_configured_repository() {
        assert!(download_url(
            "https://github.com/owner/repo/releases/download/v1/a.zip",
            "owner/repo"
        )
        .is_ok());
        for url in [
            "http://github.com/owner/repo/releases/download/v1/a.zip",
            "https://example.com/owner/repo/releases/download/v1/a.zip",
            "https://github.com/other/repo/releases/download/v1/a.zip",
            "https://user@github.com/owner/repo/releases/download/v1/a.zip",
        ] {
            assert!(download_url(url, "owner/repo").is_err());
        }
    }
    #[test]
    fn checksums_require_exact_filename() {
        let hash = "a".repeat(64);
        assert_eq!(
            checksum_line(&format!("{hash}  *release.zip\r\n"), "release.zip"),
            Some(hash.clone())
        );
        assert_eq!(
            checksum_line(&format!("{hash}  other.zip"), "release.zip"),
            None
        );
        assert_eq!(checksum_line("abc  release.zip", "release.zip"), None);
    }
    #[test]
    fn download_rejects_truncation_overflow_and_bad_hash() {
        let root = Directory::new();
        let data = b"download";
        let hash = format!("{:x}", Sha256::digest(data));
        assert!(download_zip(Cursor::new(data), &root.0.join("ok"), 8, &hash).is_ok());
        assert!(download_zip(Cursor::new(data), &root.0.join("short"), 9, &hash).is_err());
        assert!(download_zip(Cursor::new(data), &root.0.join("long"), 7, &hash).is_err());
        assert!(download_zip(Cursor::new(data), &root.0.join("hash"), 8, &"0".repeat(64)).is_err());
    }
    #[test]
    fn both_package_layouts_include_native_helper() {
        for names in [
            vec![
                "MatchScope.exe",
                "simulator-api.exe",
                "Updater.exe",
                "assets/theme.txt",
            ],
            vec![
                "portable/MatchScope.exe",
                "portable/simulator-api.exe",
                "portable/Updater.exe",
                "setup.exe",
            ],
        ] {
            let root = Directory::new();
            let zip = archive(&names, &root.0);
            let dest = root.0.join("stage");
            fs::create_dir(&dest).unwrap();
            expand_portable(&zip, &dest).unwrap();
            assert!(dest.join("Updater.exe").is_file());
            assert!(!dest.join("setup.exe").exists());
        }
    }
    #[test]
    fn unsafe_or_incomplete_archives_are_rejected() {
        for extra in [
            "../escape",
            "C:/escape",
            "MATCHSCOPE.EXE",
            "NUL.txt",
            "assets/../escape",
            "assets/name. ",
            "bad:stream",
            "a//b",
        ] {
            let root = Directory::new();
            let zip = archive(
                &["MatchScope.exe", "simulator-api.exe", "Updater.exe", extra],
                &root.0,
            );
            let dest = root.0.join("stage");
            fs::create_dir(&dest).unwrap();
            assert!(expand_portable(&zip, &dest).is_err(), "{extra}");
        }
        let root = Directory::new();
        let zip = archive(&["MatchScope.exe", "simulator-api.exe"], &root.0);
        let dest = root.0.join("stage");
        fs::create_dir(&dest).unwrap();
        assert!(expand_portable(&zip, &dest).is_err());
    }
    #[test]
    fn executable_architecture_is_validated() {
        let root = Directory::new();
        assert!(assert_executable(&root.0).unwrap_err().contains("普通文件"));
        let mut bytes = executable();
        bytes[68..70].copy_from_slice(&0x014cu16.to_le_bytes());
        let path = root.0.join("wrong.exe");
        fs::write(&path, bytes).unwrap();
        assert!(assert_executable(&path).is_err());
    }
    #[test]
    fn exact_duplicate_central_entries_are_not_silently_deduplicated() {
        let root = Directory::new();
        let zip = archive(
            &[
                "MatchScope.exe",
                "simulator-api.exe",
                "Updater.exe",
                "other.exe",
                "OTHER.exe",
            ],
            &root.0,
        );
        let mut bytes = fs::read(&zip).unwrap();
        for index in 0..bytes.len() - 9 {
            if &bytes[index..index + 9] == b"OTHER.exe" {
                bytes[index..index + 9].copy_from_slice(b"other.exe");
            }
        }
        fs::write(&zip, bytes).unwrap();
        let dest = root.0.join("stage");
        fs::create_dir(&dest).unwrap();
        assert!(expand_portable(&zip, &dest).unwrap_err().contains("重复"));
    }
    #[test]
    fn links_are_rejected_even_outside_portable_tree() {
        let root = Directory::new();
        let path = root.0.join("release.zip");
        let mut writer = zip::ZipWriter::new(File::create(&path).unwrap());
        for name in REQUIRED {
            writer
                .start_file(
                    format!("portable/{name}"),
                    zip::write::SimpleFileOptions::default(),
                )
                .unwrap();
            writer.write_all(&executable()).unwrap();
        }
        writer
            .add_symlink(
                "outside",
                "portable/MatchScope.exe",
                zip::write::SimpleFileOptions::default(),
            )
            .unwrap();
        writer.finish().unwrap();
        let dest = root.0.join("stage");
        fs::create_dir(&dest).unwrap();
        assert!(expand_portable(&path, &dest)
            .unwrap_err()
            .contains("不安全"));
    }
}
