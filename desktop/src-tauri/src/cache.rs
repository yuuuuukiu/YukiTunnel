//! Stale WebView cache handling.
//!
//! Tauri pins WebKit's data directory to `~/Library/WebKit/<bundle-id>` and
//! offers no way to disable HTTP caching, so a rebuilt app can be served from
//! the previous bundle's cache and look unchanged.
//!
//! The obvious fix — delete that whole directory — also throws away
//! localStorage, which is where the saved session lives. The user then has to
//! sign in again after every upgrade. So this module deletes only the asset
//! caches and leaves the website data (localStorage, IndexedDB) alone.

use std::fs;
use std::path::PathBuf;
use std::time::SystemTime;

/// Marker recording the binary's mtime when the cache was last purged.
fn stamp_path() -> Option<PathBuf> {
    let home = std::env::var_os("HOME")?;
    Some(PathBuf::from(home).join(".yukitunnel-webview-stamp"))
}

fn binary_mtime() -> Option<SystemTime> {
    let exe = std::env::current_exe().ok()?;
    fs::metadata(exe).ok()?.modified().ok()
}

/// bundle_ids lists the identifiers this app has shipped under.
const BUNDLE_IDS: [&str; 2] = ["yukitunnel-desktop", "io.yukitunnel.desktop"];

/// asset_cache_dirs are the directories that only hold rendered resources.
///
/// `~/Library/Caches/<id>` is pure HTTP cache. Inside
/// `~/Library/WebKit/<id>` the subdirectories holding assets are
/// `WebsiteData/LocalStorage` and friends — those must survive, so only the
/// cache-ish children are listed here.
fn asset_cache_dirs() -> Vec<PathBuf> {
    let Some(home) = std::env::var_os("HOME") else {
        return Vec::new();
    };
    let home = PathBuf::from(home);
    let mut dirs = Vec::new();
    for id in BUNDLE_IDS {
        dirs.push(home.join("Library/Caches").join(id));
        let webkit = home.join("Library/WebKit").join(id);
        // These two are safe: they cache decoded resources, not app state.
        dirs.push(webkit.join("NetworkCache"));
        dirs.push(webkit.join("CacheStorage"));
    }
    dirs
}

/// purge_if_version_changed clears asset caches when the executable changed.
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

    for dir in asset_cache_dirs() {
        // Guard against a typo turning this into "delete something unrelated".
        if !dir.to_string_lossy().contains("yukitunnel") {
            continue;
        }
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
    fn never_targets_website_data() {
        for dir in asset_cache_dirs() {
            let s = dir.to_string_lossy();
            assert!(s.contains("yukitunnel"), "unrelated path: {s}");
            assert!(
                !s.contains("LocalStorage") && !s.contains("IndexedDB"),
                "must not delete persisted session data: {s}"
            );
        }
    }
}
