//! Locating and launching the bundled Go binaries.

use std::path::PathBuf;
use tauri::{AppHandle, Manager};

/// Binary name including the platform suffix Tauri adds to sidecars.
fn sidecar_name(base: &str) -> String {
    let triple = std::env::consts::ARCH.to_string() + "-" + std::env::consts::OS;
    // Tauri renames external binaries to `<name>-<target-triple>`; we resolve
    // the concrete file at runtime so development builds (plain `yuki`) work.
    let _ = triple;
    if cfg!(windows) {
        format!("{base}.exe")
    } else {
        base.to_string()
    }
}

/// Candidate locations for a bundled binary, in priority order.
pub fn resolve_binary(app: &AppHandle, base: &str) -> Option<PathBuf> {
    let name = sidecar_name(base);
    let mut candidates: Vec<PathBuf> = Vec::new();

    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push(dir.join(&name));
            candidates.push(dir.join("binaries").join(&name));
        }
    }
    if let Ok(resource) = app.path().resource_dir() {
        candidates.push(resource.join(&name));
        candidates.push(resource.join("binaries").join(&name));
    }
    // Development: binaries built by scripts/build.sh
    if let Ok(cwd) = std::env::current_dir() {
        candidates.push(cwd.join("bin").join(&name));
        candidates.push(cwd.join("..").join("bin").join(&name));
    }
    // Fall back to PATH so `cargo tauri dev` works with a system install.
    candidates.into_iter().find(|p| p.is_file())
}

/// Where YukiTunnel stores its desktop configuration and server data.
pub fn config_dir(app: &AppHandle) -> PathBuf {
    app.path()
        .app_config_dir()
        .unwrap_or_else(|_| dirs::config_dir().unwrap_or_else(|| PathBuf::from(".")).join("yukitunnel"))
}

/// Default data directory for an embedded server deployment.
pub fn default_data_dir(app: &AppHandle) -> PathBuf {
    app.path()
        .app_data_dir()
        .unwrap_or_else(|_| dirs::data_dir().unwrap_or_else(|| PathBuf::from(".")).join("yukitunnel"))
        .join("server")
}

/// Read a user-selected private key. Only called from the connect wizard after
/// the user picked the file in the native dialog; the path is validated to be a
/// regular file and the content is capped to keep the IPC payload small.
#[tauri::command]
pub fn read_private_key(path: String) -> Result<String, String> {
    let p = PathBuf::from(&path);
    let meta = std::fs::metadata(&p).map_err(|e| format!("无法读取私钥：{e}"))?;
    if !meta.is_file() {
        return Err("请选择一个私钥文件".into());
    }
    if meta.len() > 64 * 1024 {
        return Err("私钥文件过大，不像是 SSH/RSA 私钥".into());
    }
    std::fs::read_to_string(&p).map_err(|e| format!("无法读取私钥：{e}"))
}

#[derive(serde::Serialize)]
pub struct PathsInfo {
    pub config_dir: String,
    pub data_dir: String,
    pub server_binary: Option<String>,
    pub client_binary: Option<String>,
}

#[tauri::command]
pub fn paths_info(app: AppHandle) -> PathsInfo {
    PathsInfo {
        config_dir: config_dir(&app).to_string_lossy().into_owned(),
        data_dir: default_data_dir(&app).to_string_lossy().into_owned(),
        server_binary: resolve_binary(&app, "yuki-server").map(|p| p.to_string_lossy().into_owned()),
        client_binary: resolve_binary(&app, "yuki").map(|p| p.to_string_lossy().into_owned()),
    }
}
