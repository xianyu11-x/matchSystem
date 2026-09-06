//! Native updater for the Windows portable MatchScope client.
//!
//! The updater is intentionally a small, independent package.  The desktop
//! shell copies `Updater.exe` into the transaction work directory and starts
//! it with that directory as its current working directory.  The updater then
//! only uses the transaction paths after checking that they are direct
//! siblings below the installation parent.

use serde::{Deserialize, Serialize};
use serde_json::json;
use std::{
    env,
    ffi::OsStr,
    fmt,
    fs::{self, File, OpenOptions},
    io::{self, ErrorKind, Write},
    path::{Component, Path, PathBuf},
    process::Command,
    sync::atomic::{AtomicU64, Ordering},
    time::{Duration, Instant},
};

/// Current transaction protocol understood by this updater.
pub const PROTOCOL_VERSION: u32 = 1;

const OWNER_EXIT_TIMEOUT: Duration = Duration::from_secs(60);
const STARTUP_ACK_TIMEOUT: Duration = Duration::from_secs(45);
const RENAME_ATTEMPTS: usize = 40;
const RENAME_RETRY_DELAY: Duration = Duration::from_millis(250);
const STATUS_FILE: &str = ".matchscope-update-status.json";
const RECOVERY_FILE: &str = "recovery-error.json";
const ACK_ENV: &str = "MATCHSCOPE_UPDATE_ACK";
const MAIN_EXECUTABLE: &str = "MatchScope.exe";
const SIDECAR_EXECUTABLE: &str = "simulator-api.exe";

static TEMP_SEQUENCE: AtomicU64 = AtomicU64::new(0);

/// The JSON transaction passed to `Updater.exe --context`.
///
/// All fields use snake_case in JSON through the explicit serde rename rule.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub struct Transaction {
    pub protocol_version: u32,
    pub install: PathBuf,
    pub stage: PathBuf,
    pub backup: PathBuf,
    pub failed: PathBuf,
    pub pid: u32,
    pub ack: PathBuf,
    pub armed: PathBuf,
}

/// Result of a transaction.  A failed startup is a normal, recoverable
/// update outcome when the old directory was restored successfully.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ApplyOutcome {
    Succeeded,
    RolledBack { reason: String },
}

/// Errors which prevent the updater from completing a transaction.
#[derive(Debug)]
pub enum UpdateError {
    Unsupported,
    InvalidTransaction(String),
    Io(io::Error),
    Windows {
        operation: &'static str,
        code: u32,
    },
    Recovery {
        operation: String,
        source: Box<UpdateError>,
    },
}

impl fmt::Display for UpdateError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Unsupported => write!(f, "the native updater is supported on Windows only"),
            Self::InvalidTransaction(message) => write!(f, "invalid update transaction: {message}"),
            Self::Io(error) => write!(f, "I/O error: {error}"),
            Self::Windows { operation, code } => {
                write!(f, "Windows {operation} failed with error {code}")
            }
            Self::Recovery { operation, source } => write!(f, "{operation}: {source}"),
        }
    }
}

impl std::error::Error for UpdateError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Io(error) => Some(error),
            Self::Recovery { source, .. } => Some(source),
            _ => None,
        }
    }
}

impl From<io::Error> for UpdateError {
    fn from(error: io::Error) -> Self {
        Self::Io(error)
    }
}

impl From<serde_json::Error> for UpdateError {
    fn from(error: serde_json::Error) -> Self {
        Self::InvalidTransaction(format!("invalid JSON: {error}"))
    }
}

/// A validated transaction with its derived sibling directory information.
#[derive(Clone, Debug)]
pub struct ValidatedTransaction {
    pub transaction: Transaction,
    pub work: PathBuf,
    pub parent: PathBuf,
    pub lock_path: PathBuf,
}

/// Validate a transaction against the current working directory.
pub fn validate_transaction(
    transaction: &Transaction,
) -> Result<ValidatedTransaction, UpdateError> {
    let work = env::current_dir()?;
    validate_transaction_at(transaction, &work)
}

/// Validate a transaction against an explicit work directory.
///
/// This entry point is also useful to callers and tests which need to inspect
/// a transaction without changing process-global current-directory state.
pub fn validate_transaction_at(
    transaction: &Transaction,
    work: &Path,
) -> Result<ValidatedTransaction, UpdateError> {
    if transaction.protocol_version != PROTOCOL_VERSION {
        return Err(UpdateError::InvalidTransaction(format!(
            "unsupported protocol_version {}; expected {}",
            transaction.protocol_version, PROTOCOL_VERSION
        )));
    }

    let work = absolute_path(work, &env::current_dir()?)?;
    ensure_directory(&work, "work")?;
    ensure_no_reparse_tree(&work, "work")?;

    let install = require_absolute(&transaction.install, "install")?;
    let stage = require_absolute(&transaction.stage, "stage")?;
    let backup = require_absolute(&transaction.backup, "backup")?;
    let failed = require_absolute(&transaction.failed, "failed")?;
    let ack = require_absolute(&transaction.ack, "ack")?;
    let armed = require_absolute(&transaction.armed, "armed")?;

    let parent = install.parent().ok_or_else(|| {
        UpdateError::InvalidTransaction("install must have an installation parent".into())
    })?;
    let parent = absolute_path(parent, &env::current_dir()?)?;
    ensure_directory(&parent, "installation parent")?;
    ensure_no_reparse_components(&parent, false, "installation parent")?;

    let sibling_paths = [
        ("install", install.clone()),
        ("stage", stage.clone()),
        ("backup", backup.clone()),
        ("failed", failed.clone()),
        ("work", work.clone()),
    ];
    for (label, path) in &sibling_paths {
        require_direct_child(path, &parent, label)?;
        ensure_no_reparse_components(path, true, label)?;
    }
    require_distinct_paths(&sibling_paths)?;

    require_direct_child(&ack, &work, "ack")?;
    require_direct_child(&armed, &work, "armed")?;
    require_distinct_paths(&[("ack", ack.clone()), ("armed", armed.clone())])?;
    reject_existing_reparse_or_file(&ack, "ack")?;
    reject_existing_reparse_or_file(&armed, "armed")?;

    ensure_directory(&install, "install")?;
    ensure_directory(&stage, "stage")?;
    ensure_no_reparse_components(&install, false, "install")?;
    ensure_no_reparse_tree(&stage, "stage")?;
    ensure_absent(&backup, "backup")?;
    ensure_absent(&failed, "failed")?;

    require_regular_file(&stage.join(MAIN_EXECUTABLE), "stage/MatchScope.exe")?;
    require_regular_file(&stage.join(SIDECAR_EXECUTABLE), "stage/simulator-api.exe")?;
    require_regular_file(&stage.join("Updater.exe"), "stage/Updater.exe")?;

    let lock_path = parent.join(format!(
        ".{}.matchscope-update.lock",
        install.file_name().and_then(OsStr::to_str).ok_or_else(|| {
            UpdateError::InvalidTransaction("install must have a valid file name".into())
        })?
    ));
    ensure_no_reparse_components(&lock_path, true, "update lock")?;
    Ok(ValidatedTransaction {
        transaction: Transaction {
            protocol_version: transaction.protocol_version,
            install,
            stage,
            backup,
            failed,
            pid: transaction.pid,
            ack,
            armed,
        },
        work,
        parent,
        lock_path,
    })
}

/// Apply a transaction using the current working directory as `work`.
pub fn apply_transaction(transaction: &Transaction) -> Result<ApplyOutcome, UpdateError> {
    let work = env::current_dir()?;
    apply_transaction_at(transaction, &work)
}

/// Apply a transaction with an explicit work directory.
#[cfg(not(windows))]
pub fn apply_transaction_at(
    _transaction: &Transaction,
    _work: &Path,
) -> Result<ApplyOutcome, UpdateError> {
    Err(UpdateError::Unsupported)
}

/// Apply a transaction with an explicit work directory.
#[cfg(windows)]
pub fn apply_transaction_at(
    transaction: &Transaction,
    work: &Path,
) -> Result<ApplyOutcome, UpdateError> {
    let work = absolute_path(work, &env::current_dir()?)?;
    let validated = match validate_transaction_at(transaction, &work) {
        Ok(value) => value,
        Err(error) => {
            write_recovery_best_effort(&work, None, &error);
            return Err(error);
        }
    };

    let _lock = acquire_update_lock(&validated.lock_path)?;
    let tx = &validated.transaction;
    write_marker(&tx.armed)?;

    if let Err(error) = wait_for_owner_exit(tx.pid) {
        return rollback_without_swap(&validated, error);
    }

    let mut old_moved = false;
    let mut new_moved = false;
    let mut child: Option<RunningProcess> = None;
    let operation = (|| -> Result<(), UpdateError> {
        rename_with_retry(&tx.install, &tx.backup)?;
        old_moved = true;
        rename_with_retry(&tx.stage, &tx.install)?;
        new_moved = true;
        write_pending_status(&tx.install, &tx.backup)?;

        let launched = launch_updated_application(tx)?;
        child = Some(launched);
        wait_for_startup_ack(child.as_mut().expect("child was just assigned"), &tx.ack)?;
        write_success_status(&tx.install, &tx.backup)?;
        Ok(())
    })();

    match operation {
        Ok(()) => Ok(ApplyOutcome::Succeeded),
        Err(error) => {
            let reason = error.to_string();
            rollback_after_failure(&validated, &mut child, old_moved, new_moved, &reason)
        }
    }
}

/// Read and apply a transaction JSON file.
pub fn run_from_context(context: &Path) -> Result<ApplyOutcome, UpdateError> {
    #[cfg(not(windows))]
    {
        let _ = context;
        return Err(UpdateError::Unsupported);
    }

    #[cfg(windows)]
    {
        let cwd = env::current_dir()?;
        let work = absolute_path(&cwd, &cwd)?;
        let context = absolute_path(context, &cwd)?;
        if context.parent() != Some(work.as_path()) {
            let error = UpdateError::InvalidTransaction(
                "context must be a direct file inside the current work directory".into(),
            );
            write_recovery_best_effort(&work, Some(&context), &error);
            return Err(error);
        }
        ensure_no_reparse_components(&context, false, "context")?;
        require_regular_file(&context, "context")?;
        let bytes = fs::read(&context)?;
        let transaction: Transaction = match serde_json::from_slice(&bytes) {
            Ok(value) => value,
            Err(error) => {
                let update_error = UpdateError::from(error);
                write_recovery_best_effort(&work, Some(&context), &update_error);
                return Err(update_error);
            }
        };
        match apply_transaction_at(&transaction, &work) {
            Ok(outcome) => Ok(outcome),
            Err(error) => {
                write_recovery_best_effort(&work, Some(&context), &error);
                Err(error)
            }
        }
    }
}

/// Parse the only supported command-line form and execute it.
pub fn cli_main() -> i32 {
    let context = match parse_context_arg(env::args_os().skip(1)) {
        Ok(path) => path,
        Err(message) => {
            eprintln!("{message}");
            return 2;
        }
    };

    match run_from_context(&context) {
        Ok(ApplyOutcome::Succeeded) => 0,
        Ok(ApplyOutcome::RolledBack { reason }) => {
            eprintln!("update rolled back: {reason}");
            0
        }
        Err(error) => {
            eprintln!("update failed: {error}");
            1
        }
    }
}

fn parse_context_arg<I>(args: I) -> Result<PathBuf, String>
where
    I: IntoIterator,
    I::Item: Into<std::ffi::OsString>,
{
    let args: Vec<std::ffi::OsString> = args.into_iter().map(Into::into).collect();
    if args.len() == 2 && args[0] == "--context" {
        Ok(PathBuf::from(&args[1]))
    } else {
        Err("usage: Updater.exe --context <transaction.json>".into())
    }
}

fn absolute_path(path: &Path, base: &Path) -> Result<PathBuf, UpdateError> {
    if path.as_os_str().is_empty() {
        return Err(UpdateError::InvalidTransaction(
            "path must not be empty".into(),
        ));
    }
    let input = if path.is_absolute() {
        path.to_path_buf()
    } else {
        base.join(path)
    };
    let mut output = PathBuf::new();
    for component in input.components() {
        match component {
            Component::Prefix(prefix) => output.push(prefix.as_os_str()),
            Component::RootDir => output.push(component.as_os_str()),
            Component::CurDir => {}
            Component::Normal(value) => output.push(value),
            Component::ParentDir => {
                return Err(UpdateError::InvalidTransaction(format!(
                    "path contains a parent traversal: {}",
                    path.display()
                )))
            }
        }
    }
    if output.is_absolute() {
        Ok(output)
    } else {
        Err(UpdateError::InvalidTransaction(format!(
            "path is not absolute: {}",
            path.display()
        )))
    }
}

fn require_absolute(path: &Path, label: &str) -> Result<PathBuf, UpdateError> {
    if !path.is_absolute() {
        return Err(UpdateError::InvalidTransaction(format!(
            "{label} must be an absolute path"
        )));
    }
    absolute_path(path, Path::new("."))
}

fn path_key(path: &Path) -> String {
    let value = path.to_string_lossy().replace('/', "\\");
    #[cfg(windows)]
    {
        value.to_ascii_lowercase()
    }
    #[cfg(not(windows))]
    {
        value
    }
}

fn require_direct_child(path: &Path, parent: &Path, label: &str) -> Result<(), UpdateError> {
    if path.parent().map(path_key).as_deref() != Some(path_key(parent).as_str()) {
        return Err(UpdateError::InvalidTransaction(format!(
            "{label} must be an immediate sibling below the installation parent"
        )));
    }
    Ok(())
}

fn require_distinct_paths(paths: &[(&str, PathBuf)]) -> Result<(), UpdateError> {
    for (index, (left_label, left)) in paths.iter().enumerate() {
        for (right_label, right) in paths.iter().skip(index + 1) {
            if path_key(left) == path_key(right) {
                return Err(UpdateError::InvalidTransaction(format!(
                    "{left_label} and {right_label} must be different paths"
                )));
            }
        }
    }
    Ok(())
}

fn ensure_directory(path: &Path, label: &str) -> Result<(), UpdateError> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() => Ok(()),
        Ok(_) => Err(UpdateError::InvalidTransaction(format!(
            "{label} must be a directory: {}",
            path.display()
        ))),
        Err(error) if error.kind() == ErrorKind::NotFound => Err(UpdateError::InvalidTransaction(
            format!("{label} does not exist: {}", path.display()),
        )),
        Err(error) => Err(UpdateError::Io(error)),
    }
}

fn ensure_absent(path: &Path, label: &str) -> Result<(), UpdateError> {
    match fs::symlink_metadata(path) {
        Ok(_) => Err(UpdateError::InvalidTransaction(format!(
            "{label} already exists: {}",
            path.display()
        ))),
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(()),
        Err(error) => Err(UpdateError::Io(error)),
    }
}

fn require_regular_file(path: &Path, label: &str) -> Result<(), UpdateError> {
    let metadata = fs::symlink_metadata(path).map_err(|error| {
        if error.kind() == ErrorKind::NotFound {
            UpdateError::InvalidTransaction(format!("{label} is missing: {}", path.display()))
        } else {
            UpdateError::Io(error)
        }
    })?;
    if !metadata.is_file() {
        return Err(UpdateError::InvalidTransaction(format!(
            "{label} must be a regular file: {}",
            path.display()
        )));
    }
    ensure_no_reparse_components(path, false, label)
}

fn reject_existing_reparse_or_file(path: &Path, label: &str) -> Result<(), UpdateError> {
    match fs::symlink_metadata(path) {
        Ok(_) => Err(UpdateError::InvalidTransaction(format!(
            "{label} must not already exist: {}",
            path.display()
        ))),
        Err(error) if error.kind() == ErrorKind::NotFound => {
            ensure_no_reparse_components(path, true, label)
        }
        Err(error) => Err(UpdateError::Io(error)),
    }
}

fn ensure_no_reparse_tree(path: &Path, label: &str) -> Result<(), UpdateError> {
    ensure_no_reparse_components(path, false, label)?;
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_dir() {
        return Ok(());
    }
    for entry in fs::read_dir(path)? {
        let entry = entry?;
        let child = entry.path();
        ensure_no_reparse_components(&child, false, label)?;
        let child_metadata = fs::symlink_metadata(&child)?;
        if child_metadata.is_dir() {
            ensure_no_reparse_tree(&child, label)?;
        }
    }
    Ok(())
}

fn ensure_no_reparse_components(
    path: &Path,
    allow_missing_final: bool,
    label: &str,
) -> Result<(), UpdateError> {
    let mut current = PathBuf::new();
    for component in path.components() {
        current.push(component.as_os_str());
        match fs::symlink_metadata(&current) {
            Ok(metadata) => {
                if metadata.file_type().is_symlink() || is_reparse_point(&metadata) {
                    return Err(UpdateError::InvalidTransaction(format!(
                        "{label} contains a reparse point: {}",
                        current.display()
                    )));
                }
            }
            Err(error) if error.kind() == ErrorKind::NotFound && allow_missing_final => {
                if current == path {
                    break;
                }
                return Err(UpdateError::InvalidTransaction(format!(
                    "{label} has a missing parent: {}",
                    current.display()
                )));
            }
            Err(error) if error.kind() == ErrorKind::NotFound => {
                return Err(UpdateError::InvalidTransaction(format!(
                    "{label} is missing: {}",
                    current.display()
                )));
            }
            Err(error) => return Err(UpdateError::Io(error)),
        }
    }
    Ok(())
}

#[cfg(windows)]
fn is_reparse_point(metadata: &fs::Metadata) -> bool {
    use std::os::windows::fs::MetadataExt;
    const FILE_ATTRIBUTE_REPARSE_POINT: u32 = 0x0400;
    metadata.file_attributes() & FILE_ATTRIBUTE_REPARSE_POINT != 0
}

#[cfg(not(windows))]
fn is_reparse_point(_metadata: &fs::Metadata) -> bool {
    false
}

#[cfg(windows)]
fn acquire_update_lock(path: &Path) -> Result<File, UpdateError> {
    use std::os::windows::io::FromRawHandle;
    use windows_sys::Win32::{
        Foundation::{GetLastError, GENERIC_READ, GENERIC_WRITE, INVALID_HANDLE_VALUE},
        Storage::FileSystem::{CreateFileW, FILE_ATTRIBUTE_NORMAL, FILE_SHARE_NONE, OPEN_ALWAYS},
    };
    let wide = wide_path(path);
    let handle = unsafe {
        CreateFileW(
            wide.as_ptr(),
            GENERIC_READ | GENERIC_WRITE,
            FILE_SHARE_NONE,
            std::ptr::null(),
            OPEN_ALWAYS,
            FILE_ATTRIBUTE_NORMAL,
            std::ptr::null_mut(),
        )
    };
    if handle.is_null() || handle == INVALID_HANDLE_VALUE {
        let code = unsafe { GetLastError() };
        if code == 32 || code == 33 {
            return Err(UpdateError::InvalidTransaction(
                "another update transaction already holds the installation lock".into(),
            ));
        }
        return Err(UpdateError::Windows {
            operation: "CreateFileW(update lock)",
            code,
        });
    }
    Ok(unsafe { File::from_raw_handle(handle as _) })
}

#[cfg(windows)]
fn write_marker(path: &Path) -> Result<(), UpdateError> {
    let mut file = OpenOptions::new().create_new(true).write(true).open(path)?;
    file.write_all(b"ready")?;
    file.flush()?;
    file.sync_all()?;
    Ok(())
}

#[cfg(windows)]
fn wait_for_owner_exit(pid: u32) -> Result<(), UpdateError> {
    if pid == 0 {
        return Ok(());
    }
    if pid == std::process::id() {
        return Err(UpdateError::InvalidTransaction(
            "pid must identify the desktop process, not the updater".into(),
        ));
    }
    use windows_sys::Win32::{
        Foundation::{
            CloseHandle, ERROR_INVALID_PARAMETER, WAIT_FAILED, WAIT_OBJECT_0, WAIT_TIMEOUT,
        },
        System::Threading::{OpenProcess, WaitForSingleObject, PROCESS_SYNCHRONIZE},
    };
    let handle = unsafe { OpenProcess(PROCESS_SYNCHRONIZE, 0, pid) };
    if handle.is_null() {
        let code = unsafe { windows_sys::Win32::Foundation::GetLastError() };
        if code == ERROR_INVALID_PARAMETER {
            return Ok(());
        }
        return Err(UpdateError::Windows {
            operation: "OpenProcess",
            code,
        });
    }
    let result = unsafe { WaitForSingleObject(handle, OWNER_EXIT_TIMEOUT.as_millis() as u32) };
    let wait_error = if result == WAIT_FAILED {
        Some(unsafe { windows_sys::Win32::Foundation::GetLastError() })
    } else {
        None
    };
    unsafe { CloseHandle(handle) };
    match result {
        WAIT_OBJECT_0 => Ok(()),
        WAIT_TIMEOUT => Err(UpdateError::InvalidTransaction(
            "current application did not exit within 60 seconds".into(),
        )),
        WAIT_FAILED => Err(UpdateError::Windows {
            operation: "WaitForSingleObject",
            code: wait_error.unwrap_or(1),
        }),
        code => Err(UpdateError::Windows {
            operation: "WaitForSingleObject",
            code,
        }),
    }
}

#[cfg(windows)]
fn rename_with_retry(from: &Path, to: &Path) -> Result<(), UpdateError> {
    let mut last_error = None;
    for attempt in 0..RENAME_ATTEMPTS {
        match fs::rename(from, to) {
            Ok(()) => return Ok(()),
            Err(error) => {
                last_error = Some(error);
                if attempt + 1 < RENAME_ATTEMPTS {
                    std::thread::sleep(RENAME_RETRY_DELAY);
                }
            }
        }
    }
    Err(UpdateError::Io(last_error.expect("rename had one attempt")))
}

#[cfg(windows)]
fn launch_updated_application(transaction: &Transaction) -> Result<RunningProcess, UpdateError> {
    let executable = transaction.install.join(MAIN_EXECUTABLE);
    let application = wide_path(&executable);
    let mut command_line = Vec::with_capacity(application.len() + 2);
    command_line.push('"' as u16);
    command_line.extend(application.iter().copied().take(application.len() - 1));
    command_line.push('"' as u16);
    command_line.push(0);
    let directory = wide_path(&transaction.install);
    let previous_ack = env::var_os(ACK_ENV);
    env::set_var(ACK_ENV, &transaction.ack);

    use windows_sys::Win32::{
        Foundation::{CloseHandle, GetLastError, FALSE},
        System::Threading::{
            CreateProcessW, ResumeThread, CREATE_NO_WINDOW, CREATE_SUSPENDED,
            CREATE_UNICODE_ENVIRONMENT, PROCESS_INFORMATION, STARTUPINFOW,
        },
    };
    let mut startup: STARTUPINFOW = unsafe { std::mem::zeroed() };
    startup.cb = std::mem::size_of::<STARTUPINFOW>() as u32;
    let mut information: PROCESS_INFORMATION = unsafe { std::mem::zeroed() };
    let created = unsafe {
        CreateProcessW(
            application.as_ptr(),
            command_line.as_mut_ptr(),
            std::ptr::null(),
            std::ptr::null(),
            FALSE,
            CREATE_NO_WINDOW | CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT,
            std::ptr::null(),
            directory.as_ptr(),
            &startup,
            &mut information,
        )
    };
    let create_error = if created == 0 {
        Some(unsafe { GetLastError() })
    } else {
        None
    };
    match previous_ack {
        Some(value) => env::set_var(ACK_ENV, value),
        None => env::remove_var(ACK_ENV),
    }
    if let Some(code) = create_error {
        return Err(UpdateError::Windows {
            operation: "CreateProcessW",
            code,
        });
    }

    // The primary thread is suspended while it is assigned to the job. This
    // closes the race in which a short-lived new process could exit before all
    // of its descendants were covered by the process-tree kill handle.
    let job = match ProcessJob::assign(information.hProcess) {
        Ok(job) => job,
        Err(error) => {
            unsafe {
                windows_sys::Win32::System::Threading::TerminateProcess(information.hProcess, 1);
                CloseHandle(information.hThread);
                CloseHandle(information.hProcess);
            }
            return Err(error);
        }
    };
    let resume_result = unsafe { ResumeThread(information.hThread) };
    if resume_result == u32::MAX {
        let code = unsafe { GetLastError() };
        unsafe {
            windows_sys::Win32::System::JobObjects::TerminateJobObject(job.handle, 1);
            CloseHandle(information.hThread);
            CloseHandle(information.hProcess);
        }
        return Err(UpdateError::Windows {
            operation: "ResumeThread",
            code,
        });
    }
    Ok(RunningProcess {
        process: information.hProcess,
        thread: information.hThread,
        pid: information.dwProcessId,
        job,
    })
}

#[cfg(windows)]
fn wait_for_startup_ack(process: &mut RunningProcess, ack: &Path) -> Result<(), UpdateError> {
    let deadline = Instant::now() + STARTUP_ACK_TIMEOUT;
    loop {
        if startup_ack_exists(ack)? {
            return Ok(());
        }
        if let Some(status) = process.try_wait()? {
            return Err(UpdateError::InvalidTransaction(format!(
                "updated application exited before startup acknowledgement: {status}"
            )));
        }
        if Instant::now() >= deadline {
            return Err(UpdateError::InvalidTransaction(
                "updated application did not acknowledge startup within 45 seconds".into(),
            ));
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

#[cfg(windows)]
fn startup_ack_exists(path: &Path) -> Result<bool, UpdateError> {
    match fs::symlink_metadata(path) {
        Ok(metadata) => {
            if metadata.file_type().is_symlink() || is_reparse_point(&metadata) {
                return Err(UpdateError::InvalidTransaction(
                    "startup acknowledgement is a reparse point".into(),
                ));
            }
            Ok(metadata.is_file())
        }
        Err(error) if error.kind() == ErrorKind::NotFound => Ok(false),
        Err(error) => Err(UpdateError::Io(error)),
    }
}

#[cfg(windows)]
fn write_pending_status(install: &Path, backup: &Path) -> Result<(), UpdateError> {
    write_json_atomic(
        &install.join(STATUS_FILE),
        &json!({
            "ok": null,
            "pending": true,
            "message": "New version is starting; waiting for its health acknowledgement.",
            "previous": backup,
        }),
    )
}

#[cfg(windows)]
fn write_success_status(install: &Path, backup: &Path) -> Result<(), UpdateError> {
    write_json_atomic(
        &install.join(STATUS_FILE),
        &json!({
            "ok": true,
            "message": format!("Updated successfully. Previous files: {}", backup.display()),
        }),
    )
}

#[cfg(windows)]
fn write_failure_status(install: &Path, reason: &str) -> Result<(), UpdateError> {
    write_json_atomic(
        &install.join(STATUS_FILE),
        &json!({
            "ok": false,
            "message": format!("Update failed; previous version restored. {reason}"),
        }),
    )
}

#[cfg(windows)]
fn rollback_without_swap(
    validated: &ValidatedTransaction,
    error: UpdateError,
) -> Result<ApplyOutcome, UpdateError> {
    let reason = error.to_string();
    match write_failure_status(&validated.transaction.install, &reason) {
        Ok(()) => Ok(ApplyOutcome::RolledBack { reason }),
        Err(recovery_error) => Err(record_recovery_error(
            &validated.work,
            None,
            UpdateError::Recovery {
                operation: "could not write failure status".into(),
                source: Box::new(recovery_error),
            },
        )),
    }
}

#[cfg(windows)]
fn rollback_after_failure(
    validated: &ValidatedTransaction,
    child: &mut Option<RunningProcess>,
    old_moved: bool,
    new_moved: bool,
    reason: &str,
) -> Result<ApplyOutcome, UpdateError> {
    if let Some(process) = child.as_mut() {
        process.terminate_tree();
    }
    child.take();

    let tx = &validated.transaction;
    let mut recovery_error = None;
    if new_moved {
        if let Err(error) = rename_with_retry(&tx.install, &tx.failed) {
            recovery_error = Some(UpdateError::Recovery {
                operation: "could not move the failed new directory aside".into(),
                source: Box::new(error),
            });
        }
    }
    if old_moved {
        if let Err(error) = rename_with_retry(&tx.backup, &tx.install) {
            recovery_error = Some(UpdateError::Recovery {
                operation: "could not restore the previous installation directory".into(),
                source: Box::new(error),
            });
        }
    }

    if let Some(error) = recovery_error {
        return Err(record_recovery_error(&validated.work, None, error));
    }
    if let Err(error) = write_failure_status(&tx.install, reason) {
        let status_error = UpdateError::Recovery {
            operation: "previous installation was restored, but failure status could not be written".into(),
            source: Box::new(error),
        };
        return Err(record_recovery_error(&validated.work, None, status_error));
    }
    if old_moved {
        if let Err(error) = launch_recovery_application(tx) {
            let restart_error = UpdateError::Recovery {
                operation: "previous installation was restored, but it could not be restarted"
                    .into(),
                source: Box::new(error),
            };
            return Err(record_recovery_error(&validated.work, None, restart_error));
        }
    }
    Ok(ApplyOutcome::RolledBack {
        reason: reason.to_string(),
    })
}

#[cfg(windows)]
fn launch_recovery_application(transaction: &Transaction) -> Result<(), UpdateError> {
    let executable = transaction.install.join(MAIN_EXECUTABLE);
    let mut command = Command::new(&executable);
    use std::os::windows::process::CommandExt;
    command
        .current_dir(&transaction.install)
        .env_remove(ACK_ENV)
        .creation_flags(0x0800_0000);
    let _child = command.spawn()?;
    Ok(())
}

#[cfg(windows)]
fn write_json_atomic<T: Serialize>(path: &Path, value: &T) -> Result<(), UpdateError> {
    let parent = path.parent().ok_or_else(|| {
        UpdateError::InvalidTransaction(format!("status path has no parent: {}", path.display()))
    })?;
    ensure_directory(parent, "status parent")?;
    ensure_no_reparse_components(path, true, "status path")?;
    let sequence = TEMP_SEQUENCE.fetch_add(1, Ordering::Relaxed);
    let file_name = path.file_name().and_then(OsStr::to_str).ok_or_else(|| {
        UpdateError::InvalidTransaction(format!("invalid status file name: {}", path.display()))
    })?;
    let temporary = parent.join(format!(
        ".{file_name}.tmp-{}-{sequence}",
        std::process::id()
    ));
    let bytes = serde_json::to_vec_pretty(value).map_err(|error| {
        UpdateError::InvalidTransaction(format!("could not encode status JSON: {error}"))
    })?;
    {
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&temporary)?;
        file.write_all(&bytes)?;
        file.write_all(b"\n")?;
        file.flush()?;
        file.sync_all()?;
    }
    replace_file(&temporary, path)?;
    Ok(())
}

#[cfg(windows)]
fn replace_file(from: &Path, to: &Path) -> Result<(), UpdateError> {
    use windows_sys::Win32::Storage::FileSystem::{
        MoveFileExW, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH,
    };
    let from_wide = wide_path(from);
    let to_wide = wide_path(to);
    let flags = MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH;
    if unsafe { MoveFileExW(from_wide.as_ptr(), to_wide.as_ptr(), flags) } == 0 {
        return Err(last_windows_error("MoveFileExW"));
    }
    Ok(())
}

#[cfg(windows)]
fn write_recovery_best_effort(work: &Path, context: Option<&Path>, error: &UpdateError) {
    if !work.is_absolute() || !work.is_dir() {
        return;
    }
    if let Some(context) = context {
        if context.parent() != Some(work) {
            return;
        }
    }
    if ensure_no_reparse_components(work, false, "work").is_err() {
        return;
    }
    let recovery = work.join(RECOVERY_FILE);
    let _ = write_json_atomic(
        &recovery,
        &json!({
            "ok": false,
            "message": error.to_string(),
            "context": context.map(|path| path.display().to_string()),
        }),
    );
}

#[cfg(windows)]
fn record_recovery_error(work: &Path, context: Option<&Path>, error: UpdateError) -> UpdateError {
    write_recovery_best_effort(work, context, &error);
    error
}

#[cfg(windows)]
fn last_windows_error(operation: &'static str) -> UpdateError {
    let code = unsafe { windows_sys::Win32::Foundation::GetLastError() };
    UpdateError::Windows { operation, code }
}

#[cfg(windows)]
fn wide_path(path: &Path) -> Vec<u16> {
    use std::os::windows::ffi::OsStrExt;
    path.as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect()
}

#[cfg(windows)]
struct RunningProcess {
    process: windows_sys::Win32::Foundation::HANDLE,
    thread: windows_sys::Win32::Foundation::HANDLE,
    pid: u32,
    job: ProcessJob,
}

#[cfg(windows)]
impl RunningProcess {
    fn try_wait(&self) -> Result<Option<u32>, UpdateError> {
        use windows_sys::Win32::{
            Foundation::{WAIT_OBJECT_0, WAIT_TIMEOUT},
            System::Threading::{GetExitCodeProcess, WaitForSingleObject},
        };
        let wait = unsafe { WaitForSingleObject(self.process, 0) };
        if wait == WAIT_TIMEOUT {
            return Ok(None);
        }
        if wait != WAIT_OBJECT_0 {
            return Err(last_windows_error("WaitForSingleObject"));
        }
        let mut code = 259u32; // STILL_ACTIVE
        if unsafe { GetExitCodeProcess(self.process, &mut code) } == 0 {
            return Err(last_windows_error("GetExitCodeProcess"));
        }
        Ok(Some(code))
    }

    fn terminate_tree(&mut self) {
        unsafe {
            if windows_sys::Win32::System::JobObjects::TerminateJobObject(self.job.handle, 1) == 0 {
                terminate_process_tree(self.pid);
            }
            // TerminateJobObject is asynchronous. Give Windows a bounded
            // opportunity to release executable handles before directory
            // replacement starts; rename retries remain the final shield.
            windows_sys::Win32::System::Threading::WaitForSingleObject(self.process, 5_000);
            windows_sys::Win32::Foundation::CloseHandle(self.process);
        }
        self.process = std::ptr::null_mut();
    }
}

#[cfg(windows)]
impl Drop for RunningProcess {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.thread);
            if !self.process.is_null() {
                windows_sys::Win32::Foundation::CloseHandle(self.process);
            }
        }
    }
}

#[cfg(windows)]
struct ProcessJob {
    handle: windows_sys::Win32::Foundation::HANDLE,
}

#[cfg(windows)]
impl ProcessJob {
    fn assign(process: windows_sys::Win32::Foundation::HANDLE) -> Result<Self, UpdateError> {
        use windows_sys::Win32::{
            Foundation::{CloseHandle, HANDLE},
            System::JobObjects::{AssignProcessToJobObject, CreateJobObjectW},
        };
        let job = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
        if job.is_null() {
            return Err(last_windows_error("CreateJobObjectW"));
        }
        let assigned = unsafe { AssignProcessToJobObject(job, process) } != 0;
        if !assigned {
            let code = unsafe { windows_sys::Win32::Foundation::GetLastError() };
            unsafe { CloseHandle(job) };
            return Err(UpdateError::Windows {
                operation: "AssignProcessToJobObject",
                code,
            });
        }
        Ok(Self {
            handle: job as HANDLE,
        })
    }
}

#[cfg(windows)]
impl Drop for ProcessJob {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.handle);
        }
    }
}

#[cfg(windows)]
fn terminate_process_tree(root: u32) {
    use windows_sys::Win32::{
        Foundation::{CloseHandle, FALSE, INVALID_HANDLE_VALUE},
        System::{
            Diagnostics::ToolHelp::{
                CreateToolhelp32Snapshot, Process32FirstW, Process32NextW, PROCESSENTRY32W,
                TH32CS_SNAPPROCESS,
            },
            Threading::{OpenProcess, TerminateProcess, PROCESS_TERMINATE},
        },
    };
    let snapshot = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0) };
    if snapshot == INVALID_HANDLE_VALUE {
        return;
    }
    let mut entries = Vec::new();
    let mut entry = PROCESSENTRY32W {
        dwSize: std::mem::size_of::<PROCESSENTRY32W>() as u32,
        ..unsafe { std::mem::zeroed() }
    };
    let mut first = unsafe { Process32FirstW(snapshot, &mut entry) } != 0;
    while first {
        entries.push((entry.th32ProcessID, entry.th32ParentProcessID));
        first = unsafe { Process32NextW(snapshot, &mut entry) } != 0;
    }
    unsafe { CloseHandle(snapshot) };

    let mut descendants = Vec::new();
    let mut pending = vec![root];
    while let Some(parent) = pending.pop() {
        for (pid, parent_pid) in &entries {
            if *parent_pid == parent && *pid != root && !descendants.contains(pid) {
                descendants.push(*pid);
                pending.push(*pid);
            }
        }
    }
    descendants.push(root);
    for pid in descendants.into_iter().rev() {
        let handle = unsafe { OpenProcess(PROCESS_TERMINATE, FALSE, pid) };
        if !handle.is_null() {
            unsafe {
                let _ = TerminateProcess(handle, 1);
                CloseHandle(handle);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_root() -> PathBuf {
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock before epoch")
            .as_nanos();
        env::temp_dir().join(format!("matchscope-updater-test-{nonce}"))
    }

    fn valid_fixture() -> (PathBuf, Transaction) {
        let root = temp_root();
        let parent = root.join("parent");
        let install = parent.join("install");
        let stage = parent.join("work-stage");
        let work = parent.join("work");
        fs::create_dir_all(&install).expect("install");
        fs::create_dir_all(&stage).expect("stage");
        fs::create_dir_all(&work).expect("work");
        fs::write(stage.join(MAIN_EXECUTABLE), b"MZ fixture").expect("main fixture");
        fs::write(stage.join(SIDECAR_EXECUTABLE), b"MZ fixture").expect("sidecar fixture");
        fs::write(stage.join("Updater.exe"), b"MZ fixture").expect("updater fixture");
        let transaction = Transaction {
            protocol_version: PROTOCOL_VERSION,
            install,
            stage,
            backup: parent.join("work-backup"),
            failed: parent.join("work-failed"),
            pid: 0,
            ack: work.join("ack"),
            armed: work.join("armed"),
        };
        (root, transaction)
    }

    #[test]
    fn transaction_json_uses_snake_case() {
        let (root, transaction) = valid_fixture();
        let value = serde_json::to_value(&transaction).expect("serialize transaction");
        assert!(value.get("protocol_version").is_some());
        assert!(value.get("install").is_some());
        assert!(value.get("stage").is_some());
        assert!(value.get("backup").is_some());
        assert!(value.get("failed").is_some());
        assert!(value.get("pid").is_some());
        assert!(value.get("ack").is_some());
        assert!(value.get("armed").is_some());
        assert!(value.get("protocolVersion").is_none());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn validation_accepts_a_complete_sibling_transaction() {
        let (root, transaction) = valid_fixture();
        let work = root.join("parent/work");
        let validated = validate_transaction_at(&transaction, &work).expect("valid transaction");
        assert_eq!(validated.work, work);
        assert_eq!(validated.parent, root.join("parent"));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn validation_rejects_protocol_mismatch() {
        let (root, mut transaction) = valid_fixture();
        transaction.protocol_version = PROTOCOL_VERSION + 1;
        let work = root.join("parent/work");
        assert!(matches!(
            validate_transaction_at(&transaction, &work),
            Err(UpdateError::InvalidTransaction(message)) if message.contains("protocol_version")
        ));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn validation_rejects_preexisting_acknowledgement() {
        let (root, transaction) = valid_fixture();
        fs::write(&transaction.ack, b"stale").expect("stale ack");
        let work = root.join("parent/work");
        assert!(validate_transaction_at(&transaction, &work).is_err());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn validation_rejects_duplicate_or_external_directories() {
        let (root, mut transaction) = valid_fixture();
        let work = root.join("parent/work");
        let valid = transaction.clone();
        transaction.failed = transaction.backup.clone();
        assert!(validate_transaction_at(&transaction, &work).is_err());
        let mut transaction = valid;
        transaction.stage = root.join("outside");
        assert!(validate_transaction_at(&transaction, &work).is_err());
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn validation_rejects_parent_traversal_and_work_mismatch() {
        let (root, transaction) = valid_fixture();
        let work = root.join("parent/work");
        let mut traversal = transaction.clone();
        traversal.install = root.join("parent/../outside/install");
        assert!(validate_transaction_at(&traversal, &work).is_err());
        assert!(validate_transaction_at(&transaction, &root).is_err());
        let _ = fs::remove_dir_all(root);
    }

    #[cfg(windows)]
    #[test]
    fn same_level_update_lock_rejects_concurrent_transaction() {
        let (root, transaction) = valid_fixture();
        let lock_path = root.join("parent/.install.matchscope-update.lock");
        let first = acquire_update_lock(&lock_path).expect("first lock");
        assert!(acquire_update_lock(&lock_path).is_err());
        drop(first);
        assert!(acquire_update_lock(&lock_path).is_ok());
        let _ = fs::remove_dir_all(root);
        let _ = transaction;
    }

    #[test]
    fn validation_rejects_missing_next_updater() {
        let (root, transaction) = valid_fixture();
        fs::remove_file(transaction.stage.join("Updater.exe")).expect("remove next helper");
        assert!(validate_transaction_at(&transaction, &root.join("parent/work")).is_err());
        let _ = fs::remove_dir_all(root);
    }

    #[cfg(not(windows))]
    #[test]
    fn non_windows_apply_is_explicitly_unsupported() {
        let (root, transaction) = valid_fixture();
        assert!(matches!(
            apply_transaction_at(&transaction, Path::new(".")),
            Err(UpdateError::Unsupported)
        ));
        let _ = fs::remove_dir_all(root);
    }
}
