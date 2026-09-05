fn main() {
    if let Ok(output) = std::process::Command::new("git")
        .args(["rev-parse", "--git-common-dir"])
        .output()
    {
        if output.status.success() {
            let directory = String::from_utf8_lossy(&output.stdout);
            println!("cargo:rerun-if-changed={}/config", directory.trim());
        }
    }
    println!("cargo:rerun-if-env-changed=MATCHSCOPE_UPDATE_REPOSITORY");
    let repository = std::env::var("MATCHSCOPE_UPDATE_REPOSITORY")
        .ok()
        .or_else(|| {
            let output = std::process::Command::new("git")
                .args(["remote", "get-url", "origin"])
                .output()
                .ok()?;
            if !output.status.success() {
                return None;
            }
            let remote = String::from_utf8(output.stdout).ok()?;
            let remote = remote.trim().trim_end_matches(".git");
            remote
                .strip_prefix("https://github.com/")
                .or_else(|| remote.strip_prefix("git@github.com:"))
                .map(str::to_owned)
        })
        .expect("Set MATCHSCOPE_UPDATE_REPOSITORY=owner/repo or configure a GitHub origin remote");
    assert!(
        repository.split('/').count() == 2
            && repository.split('/').all(|part| !part.is_empty()
                && part
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b))),
        "Invalid GitHub update repository"
    );
    println!("cargo:rustc-env=MATCHSCOPE_UPDATE_REPOSITORY={repository}");
    tauri_build::build()
}
