//! Supervises the bundled `yuki run` client process.
//!
//! The desktop client keeps the same behaviour as the CLI: one long-lived
//! agent process that reconnects automatically and forwards every tunnel the
//! user starts. The UI only toggles this process and shows its log stream.

use std::process::Stdio;
use std::sync::Mutex;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};

use crate::sidecar::resolve_binary;

#[derive(Default)]
pub struct AgentState {
    pub child: Mutex<Option<Child>>,
    pub running: Mutex<bool>,
    pub last_error: Mutex<Option<String>>,
    pub log_path: Mutex<Option<std::path::PathBuf>>,
}

#[derive(Deserialize)]
pub struct AgentStartRequest {
    pub server_url: String,
    pub token: Option<String>,
    pub username: Option<String>,
    pub key_path: Option<String>,
    pub insecure: bool,
    /// When set, only these tunnel IDs are started; otherwise all are.
    pub tunnel_ids: Vec<String>,
    pub auto_reconnect: bool,
}

#[derive(Serialize)]
pub struct AgentStatus {
    pub running: bool,
    pub pid: Option<u32>,
    pub last_error: Option<String>,
    pub log_path: Option<String>,
}

#[derive(Serialize, Clone)]
struct AgentEvent {
    kind: String,
    message: String,
}

/// Start the agent. Reuses the same config directory as the CLI so the user
/// never has to re-enter credentials.
#[tauri::command]
pub async fn agent_start(app: AppHandle, state: State<'_, AgentState>, req: AgentStartRequest) -> Result<AgentStatus, String> {
    if *state.running.lock().unwrap() {
        return agent_status_inner(&state);
    }
    let binary = resolve_binary(&app, "yuki").ok_or("找不到 yuki 客户端组件，请重新安装")?;
    let log_dir = app
        .path()
        .app_log_dir()
        .unwrap_or_else(|_| std::env::temp_dir().join("yukitunnel"));
    std::fs::create_dir_all(&log_dir).ok();
    let log_path = log_dir.join("agent.log");
    // Touch the file so the path exists before the first line arrives.
    std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)
        .map_err(|e| format!("无法创建日志文件：{e}"))?;

    let mut cmd = Command::new(&binary);
    cmd.arg("run").arg("--server").arg(&req.server_url);
    if let Some(token) = &req.token {
        if !token.is_empty() {
            cmd.arg("--token").arg(token);
        }
    }
    if let Some(user) = &req.username {
        if !user.is_empty() {
            cmd.arg("--username").arg(user);
        }
    }
    if let Some(key) = &req.key_path {
        if !key.is_empty() {
            cmd.arg("--key").arg(key);
        }
    }
    if req.insecure {
        cmd.arg("--insecure");
    }
    if req.auto_reconnect {
        cmd.arg("--all");
    }
    for id in &req.tunnel_ids {
        cmd.arg("--tunnel").arg(id);
    }
    cmd.arg("--json");
    cmd.stdout(Stdio::piped()).stderr(Stdio::piped()).kill_on_drop(false);

    let mut child = cmd.spawn().map_err(|e| format!("启动客户端失败：{e}"))?;
    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    let pid = child.id();

    if let Some(out) = stdout {
        forward(app.clone(), out, log_path.clone(), "stdout");
    }
    if let Some(err) = stderr {
        forward(app.clone(), err, log_path.clone(), "stderr");
    }

    *state.child.lock().unwrap() = Some(child);
    *state.running.lock().unwrap() = true;
    *state.last_error.lock().unwrap() = None;
    *state.log_path.lock().unwrap() = Some(log_path.clone());

    // Supervise: report unexpected exits so the UI can offer "reconnect".
    let app2 = app.clone();
    tokio::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(2)).await;
            let st = app2.state::<AgentState>();
            let mut guard = st.child.lock().unwrap();
            match guard.as_mut() {
                Some(c) => match c.try_wait() {
                    Ok(Some(status)) => {
                        *guard = None;
                        *st.running.lock().unwrap() = false;
                        let _ = app2.emit(
                            "agent://state",
                            AgentEvent { kind: "exited".into(), message: format!("客户端已退出（{status}）") },
                        );
                        break;
                    }
                    Ok(None) => {}
                    Err(_) => break,
                },
                None => {
                    *st.running.lock().unwrap() = false;
                    break;
                }
            }
        }
    });

    Ok(AgentStatus { running: true, pid, last_error: None, log_path: Some(log_path.to_string_lossy().into_owned()) })
}

fn forward<R>(app: AppHandle, reader: R, log_path: std::path::PathBuf, stream: &'static str)
where
    R: tokio::io::AsyncRead + Unpin + Send + 'static,
{
    tokio::spawn(async move {
        use std::io::Write;
        let mut lines = BufReader::new(reader).lines();
        while let Ok(Some(line)) = lines.next_line().await {
            let _ = app.emit("agent://log", AgentEvent { kind: stream.into(), message: line.clone() });
            if let Ok(mut f) = std::fs::OpenOptions::new().create(true).append(true).open(&log_path) {
                let _ = writeln!(f, "{line}");
            }
        }
    });
}

#[tauri::command]
pub async fn agent_stop(state: State<'_, AgentState>) -> Result<AgentStatus, String> {
    let mut child = { state.child.lock().unwrap().take() };
    if let Some(child) = child.as_mut() {
        child.start_kill().map_err(|e| e.to_string())?;
        let _ = tokio::time::timeout(Duration::from_secs(5), child.wait()).await;
    }
    drop(child);
    *state.running.lock().unwrap() = false;
    agent_status_inner(&state)
}

fn agent_status_inner(state: &State<'_, AgentState>) -> Result<AgentStatus, String> {
    let running = *state.running.lock().unwrap();
    let pid = state.child.lock().unwrap().as_ref().and_then(|c| c.id());
    Ok(AgentStatus {
        running,
        pid,
        last_error: state.last_error.lock().unwrap().clone(),
        log_path: state.log_path.lock().unwrap().as_ref().map(|p| p.to_string_lossy().into_owned()),
    })
}

#[tauri::command]
pub async fn agent_status(state: State<'_, AgentState>) -> Result<AgentStatus, String> {
    agent_status_inner(&state)
}

#[tauri::command]
pub async fn agent_logs(state: State<'_, AgentState>, lines: Option<usize>) -> Result<String, String> {
    let path = state.log_path.lock().unwrap().clone();
    let path = match path {
        Some(p) => p,
        None => return Ok(String::new()),
    };
    let content = std::fs::read_to_string(&path).unwrap_or_default();
    let all: Vec<&str> = content.lines().collect();
    let start = all.len().saturating_sub(lines.unwrap_or(200));
    Ok(all[start..].join("\n"))
}
