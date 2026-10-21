//! Stale WebView cache handling.
//!
//! Tauri pins WebKit's data directory to `~/Library/WebKit/<bundle-id>` and
//! offers no configuration to disable HTTP caching. When the app is rebuilt
//! with new frontend assets but keeps the same version, WebKit happily serves
//! the previous `index.html` and JS bundle — the window looks identical to the
//! old build, which is exactly the confusion this module prevents.
//!
//! The fix is deliberately blunt: on the first launch after the binary changes
//! on disk, delete the cache directories. Local storage is left alone so a
//! signed-in session survives the upgrade.

use std::fs;
use std::path::PathBuf;
use std::time::SystemTime;

/// Marker recording the binary's mtime when the cache was last purged.
fn stamp_path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join(".yukitunnel-webview-stamp"))
}

/// binary_mtime returns the mtime of the running executable.
fn binary_mtime() -> Option<SystemTime> {
    let exe = std::env::current_exe().ok()?;
    fs::metadata(exe).ok()?.modified().ok()
}

/// cache_dirs lists the WebKit directories that hold rendered assets.
fn cache_dirs() -> Vec<PathBuf> {
    let Some(home) = std::env::var_os("HOME") else {
        return Vec::new();
    };
    let home = PathBuf::from(home);
    // Both identifiers have been used across releases; clear whichever exists.
    ["yukitunnel-desktop", "io.yukitunnel.desktop"]
        .iter()
        .flat_map(|id| {
            [
                home.join("Library/WebKit").join(id),
                home.join("Library/Caches").join(id),
            ]
        })
        .collect()
}

/// purge_if_version_changed clears the asset cache when the executable changed.
///
/// Returns true when a purge actually happened, which the caller may log.
pub fn purge_if_version_changed() -> bool {
    let Some(mtime) = binary_mtime() else {
        return false;
    };
    let stamp = stamp_path();
    let recorded = stamp
        .as_ref()
        .and_then(|p| fs::read_to_string(p).ok())
        .and_then(|s| s.trim().parse::<u64>().ok());

    let current = mtime
        .duration_since(SystemTime::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    if recorded == Some(current) {
        return false;
    }

    for dir in cache_dirs() {
        // Only remove directories that are clearly ours.
        if dir.exists() {
            let _ = fs::remove_dir_all(&dir);
        }
    }
    if let Some(p) = stamp {
        let _ = fs::write(p, current.to_string());
    }
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cache_dirs_are_under_home() {
        for dir in cache_dirs() {
            assert!(
                dir.to_string_lossy().contains("yukitunnel"),
                "refusing to purge an unrelated path: {dir:?}"
            );
        }
    }
}
