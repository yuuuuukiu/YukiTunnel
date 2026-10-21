//! YukiTunnel desktop shell.
//!
//! Two responsibilities live here:
//!
//! 1. **Server console / one-click deploy** — detect the environment, run the
//!    bundled `yuki-server` binary, stream progress to the UI and verify health.
//! 2. **Client agent supervision** — start/stop the bundled `yuki` client in
//!    `run` mode, forward its log lines to the UI and restart it on crash.
//!
//! All long-running work is done in child processes so the UI never blocks and
//! a crash in the agent cannot take the window down.

mod agent;
mod api;
mod deploy;
mod sidecar;

use tauri::Manager;

pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .manage(deploy::DeployState::default())
        .manage(agent::AgentState::default())
        .setup(|app| {
            let handle = app.handle().clone();
            let tray = tauri::tray::TrayIconBuilder::with_id("main")
                .tooltip("YukiTunnel")
                .icon(app.default_window_icon().unwrap().clone())
                .on_tray_icon_event(|tray, event| {
                    if let tauri::tray::TrayIconEvent::Click {
                        button: tauri::tray::MouseButton::Left,
                        button_state: tauri::tray::MouseButtonState::Up,
                        ..
                    } = event
                    {
                        if let Some(win) = tray.app_handle().get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                })
                .build(app)?;
            let _ = tray;
            let _ = handle;
            Ok(())
        })
        .on_window_event(|window, event| {
            // Closing the window hides it to the tray; the agent keeps running.
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                if window.label() == "main" {
                    let _ = window.hide();
                    api.prevent_close();
                }
            }
        })
        .invoke_handler(tauri::generate_handler![
            deploy::env_check,
            deploy::deploy_server,
            deploy::server_status,
            deploy::stop_server,
            deploy::server_logs,
            deploy::install_notes,
            agent::agent_start,
            agent::agent_stop,
            agent::agent_status,
            agent::agent_logs,
            sidecar::paths_info,
            sidecar::read_private_key,
            api::api_request,
        ])
        .run(tauri::generate_context!())
        .expect("error while running YukiTunnel");
}
