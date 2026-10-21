//! One-click server deployment for the desktop app.
//!
//! The wizard collects: data directory, API/control ports, public host, TLS
//! mode, admin credentials and the domain switch. `deploy_server` then
//!  1. re-runs the environment check,
//!  2. calls `yuki-server init` (idempotent) to write config + TLS + admin,
//!  3. starts `yuki-server start` as a detached child,
//!  4. polls `/api/v1/health` until it answers and reports the certificate
//!     fingerprint so the UI can show a trust prompt.
//!
//! Every step emits a `deploy://progress` event with a step id, human message
//! and optional detail, so the wizard renders a real progress list instead of a
//! spinner.

use std::path::{Path, PathBuf};
use std::process::Stdio;
use std::sync::Mutex;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, State};
use tokio::process::Command;

use crate::sidecar::{default_data_dir, resolve_binary};

/// Shared deployment state, guarded by a mutex because Tauri commands run on a
/// thread pool.
#[derive(Default)]
pub struct DeployState {
    pub child: Mutex<Option<tokio::process::Child>>,
    pub last_error: Mutex<Option<String>>,
    pub running: Mutex<bool>,
    pub data_dir: Mutex<Option<PathBuf>>,
}

#[derive(Serialize)]
pub struct EnvReport {
    pub os: String,
    pub arch: String,
    pub binary_found: bool,
    pub binary_path: Option<String>,
    pub binary_version: Option<String>,
    pub data_dir: String,
    pub data_dir_writable: bool,
    pub api_port_free: bool,
    pub control_port_free: bool,
    pub docker_available: bool,
    pub existing_deployment: bool,
    pub checks: Vec<CheckItem>,
    pub ready: bool,
}

#[derive(Serialize, Clone)]
pub struct CheckItem {
    pub id: String,
    pub label: String,
    pub ok: bool,
    pub detail: String,
    pub fatal: bool,
}

#[derive(Serialize, Clone)]
struct Progress {
    step: String,
    status: String,
    message: String,
    detail: Option<String>,
    percent: u8,
}

fn emit(app: &AppHandle, step: &str, status: &str, message: &str, detail: Option<String>, percent: u8) {
    let _ = app.emit(
        "deploy://progress",
        Progress {
            step: step.into(),
            status: status.into(),
            message: message.into(),
            detail,
            percent,
        },
    );
}

fn port_free(port: u16) -> bool {
    std::net::TcpListener::bind(("0.0.0.0", port)).is_ok()
}

fn dir_writable(dir: &Path) -> bool {
    if std::fs::create_dir_all(dir).is_err() {
        return false;
    }
    let probe = dir.join(".yuki-write-test");
    match std::fs::write(&probe, b"ok") {
        Ok(_) => {
            let _ = std::fs::remove_file(&probe);
            true
        }
        Err(_) => false,
    }
}

/// Environment detection shown before the user commits to a deployment.
#[tauri::command]
pub async fn env_check(app: AppHandle, data_dir: Option<String>, api_port: Option<u16>, control_port: Option<u16>) -> Result<EnvReport, String> {
    let dir = data_dir.map(PathBuf::from).unwrap_or_else(|| default_data_dir(&app));
    let api_port = api_port.unwrap_or(8443);
    let control_port = control_port.unwrap_or(9443);
    let binary = resolve_binary(&app, "yuki-server");

    let mut checks = Vec::new();
    let mut push = |id: &str, label: &str, ok: bool, detail: String, fatal: bool| {
        checks.push(CheckItem { id: id.into(), label: label.into(), ok, detail, fatal });
    };

    let binary_found = binary.is_some();
    push(
        "binary",
        "服务端组件",
        binary_found,
        match &binary {
            Some(p) => format!("已找到 {}", p.display()),
            None => "未找到 yuki-server，请重新安装或把二进制放到应用目录".into(),
        },
        true,
    );

    let mut binary_version = None;
    if let Some(path) = &binary {
        if let Ok(out) = Command::new(path).arg("version").output().await {
            let text = String::from_utf8_lossy(&out.stdout).trim().to_string();
            if !text.is_empty() {
                binary_version = Some(text);
            }
        }
    }

    let writable = dir_writable(&dir);
    push(
        "data_dir",
        "数据目录",
        writable,
        if writable { dir.display().to_string() } else { format!("不可写：{}", dir.display()) },
        true,
    );

    let api_free = port_free(api_port);
    let control_free = port_free(control_port);
    push("api_port", &format!("API 端口 {api_port}"), api_free, if api_free { "可用".into() } else { "已被占用".into() }, true);
    push(
        "control_port",
        &format!("控制端口 {control_port}"),
        control_free,
        if control_free { "可用".into() } else { "已被占用".into() },
        true,
    );

    let docker = Command::new("docker").arg("--version").output().await.map(|o| o.status.success()).unwrap_or(false);
    push(
        "docker",
        "Docker（可选）",
        docker,
        if docker { "已安装，可改用容器部署".into() } else { "未安装，将使用内置服务端".into() },
        false,
    );

    let existing = dir.join("config.yaml").exists() && dir.join("yukitunnel.db").exists();
    push(
        "existing",
        "已有部署",
        true,
        if existing { "检测到已有配置，将复用数据并保留现有账号".into() } else { "全新安装".into() },
        false,
    );

    let ready = checks.iter().filter(|c| c.fatal).all(|c| c.ok);
    Ok(EnvReport {
        os: std::env::consts::OS.into(),
        arch: std::env::consts::ARCH.into(),
        binary_found,
        binary_path: binary.map(|p| p.to_string_lossy().into_owned()),
        binary_version,
        data_dir: dir.to_string_lossy().into_owned(),
        data_dir_writable: writable,
        api_port_free: api_free,
        control_port_free: control_free,
        docker_available: docker,
        existing_deployment: existing,
        checks,
        ready,
    })
}

#[derive(Deserialize)]
pub struct DeployRequest {
    pub data_dir: String,
    pub api_port: u16,
    pub control_port: u16,
    pub public_host: String,
    pub admin_user: String,
    pub admin_password: String,
    pub domain_binding: bool,
    pub domain_suffix: String,
    pub port_min: u16,
    pub port_max: u16,
    /// "self-signed" or "existing"
    pub tls_mode: String,
    pub tls_cert: Option<String>,
    pub tls_key: Option<String>,
}

#[derive(Serialize)]
pub struct DeployResult {
    pub ok: bool,
    pub webui_url: String,
    pub control_addr: String,
    pub fingerprint: Option<String>,
    pub admin_user: String,
    pub generated_password: Option<String>,
    pub data_dir: String,
    pub log_file: Option<String>,
}

/// Run the full deployment. Emits `deploy://progress` events throughout.
#[tauri::command]
pub async fn deploy_server(app: AppHandle, state: State<'_, DeployState>, req: DeployRequest) -> Result<DeployResult, String> {
    if *state.running.lock().unwrap() {
        return Err("已有部署任务正在进行".into());
    }
    *state.running.lock().unwrap() = true;
    *state.last_error.lock().unwrap() = None;
    let guard = DeployGuard { state: state.inner() };
    let result = deploy_inner(&app, &req).await;
    if let Err(err) = &result {
        *guard.state.last_error.lock().unwrap() = Some(err.clone());
    }
    drop(guard);
    result
}

/// Clears the running flag even when the deployment fails early.
struct DeployGuard<'a> {
    state: &'a DeployState,
}

impl Drop for DeployGuard<'_> {
    fn drop(&mut self) {
        *self.state.running.lock().unwrap() = false;
    }
}

async fn deploy_inner(app: &AppHandle, req: &DeployRequest) -> Result<DeployResult, String> {
    let binary = resolve_binary(app, "yuki-server").ok_or("找不到 yuki-server 可执行文件")?;
    let data_dir = PathBuf::from(&req.data_dir);
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("无法创建数据目录：{e}"))?;

    emit(app, "validate", "running", "检查端口与目录…", None, 5);
    if !port_free(req.api_port) {
        emit(app, "validate", "error", "API 端口被占用", Some(format!("{} 已被其他程序使用", req.api_port)), 5);
        return Err(format!("API 端口 {} 已被占用，请更换后重试", req.api_port));
    }
    if !port_free(req.control_port) {
        return Err(format!("控制端口 {} 已被占用，请更换后重试", req.control_port));
    }
    emit(app, "validate", "done", "端口与目录可用", None, 10);

    emit(app, "tls", "running", "准备 TLS 证书…", None, 20);
    let mut init = Command::new(&binary);
    init.arg("init")
        .arg("--data-dir")
        .arg(&data_dir)
        .arg("--api-addr")
        .arg(format!("0.0.0.0:{}", req.api_port))
        .arg("--control-addr")
        .arg(format!("0.0.0.0:{}", req.control_port))
        .arg("--public-host")
        .arg(&req.public_host)
        .arg("--admin-user")
        .arg(&req.admin_user)
        .arg("--port-min")
        .arg(req.port_min.to_string())
        .arg("--port-max")
        .arg(req.port_max.to_string())
        .arg("--json");
    if !req.admin_password.is_empty() {
        init.arg("--admin-password").arg(&req.admin_password);
    }
    if !req.domain_suffix.is_empty() {
        init.arg("--domain-suffix").arg(&req.domain_suffix);
    }
    if req.domain_binding {
        init.arg("--domain-binding");
    }
    if req.tls_mode == "existing" {
        if let (Some(cert), Some(key)) = (&req.tls_cert, &req.tls_key) {
            init.arg("--tls-cert").arg(cert).arg("--tls-key").arg(key);
        } else {
            return Err("选择已有证书时必须提供证书与私钥路径".into());
        }
    }
    let out = init.output().await.map_err(|e| format!("启动初始化失败：{e}"))?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        let stdout = String::from_utf8_lossy(&out.stdout);
        let detail = format!("{stdout}\n{stderr}");
        emit(app, "tls", "error", "初始化失败", Some(detail.clone()), 20);
        return Err(format!("初始化失败：{}", detail.trim()));
    }
    let init_json: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap_or(serde_json::json!({}));
    let fingerprint = init_json
        .get("tls_fingerprint")
        .and_then(|v| v.as_str())
        .map(|s| s.to_string());
    let admin_created = init_json.get("admin_created").and_then(|v| v.as_bool()).unwrap_or(false);
    emit(app, "tls", "done", "TLS 就绪", fingerprint.clone(), 30);

    emit(app, "admin", "done", "管理员账号已就绪", Some(req.admin_user.clone()), 40);

    emit(app, "start", "running", "启动服务端进程…", None, 50);
    let log_dir = data_dir.join("logs");
    std::fs::create_dir_all(&log_dir).ok();
    let log_path = log_dir.join("desktop-server.log");
    let log = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)
        .map_err(|e| format!("无法打开日志文件：{e}"))?;
    let log_err = log.try_clone().map_err(|e| e.to_string())?;

    let mut child = Command::new(&binary)
        .arg("serve")
        .arg("--data-dir")
        .arg(&data_dir)
        .current_dir(&data_dir)
        .stdout(Stdio::from(log))
        .stderr(Stdio::from(log_err))
        .kill_on_drop(false)
        .spawn()
        .map_err(|e| format!("无法启动服务端：{e}"))?;
    let pid = child.id();
    emit(app, "start", "done", "服务端进程已启动", pid.map(|p| format!("PID {p}")), 60);

    // Stream the log tail into the UI while we wait for health.
    let app_for_log = app.clone();
    let log_path_for_tail = log_path.clone();
    tokio::spawn(async move {
        let mut offset = 0u64;
        for _ in 0..120 {
            tokio::time::sleep(Duration::from_millis(500)).await;
            if let Ok(content) = tokio::fs::read(&log_path_for_tail).await {
                if content.len() as u64 > offset {
                    let chunk = String::from_utf8_lossy(&content[offset as usize..]).to_string();
                    offset = content.len() as u64;
                    let _ = app_for_log.emit("deploy://log", chunk);
                }
            }
        }
    });

    emit(app, "health", "running", "等待健康检查…", None, 70);
    let url = format!("https://127.0.0.1:{}/api/v1/health", req.api_port);
    let client = reqwest::Client::builder()
        .danger_accept_invalid_certs(true)
        .timeout(Duration::from_secs(3))
        .build()
        .map_err(|e| e.to_string())?;
    let mut healthy = false;
    for attempt in 0..40 {
        // The child may exit immediately (e.g. port race); detect that early.
        if let Ok(Some(status)) = child.try_wait() {
            let detail = read_tail(&log_path, 4000);
            emit(app, "health", "error", "服务端进程已退出", Some(detail.clone()), 70);
            return Err(format!("服务端进程退出（{status}）：\n{detail}"));
        }
        if let Ok(resp) = client.get(&url).send().await {
            if resp.status().is_success() {
                healthy = true;
                break;
            }
        }
        let pct = 70 + (attempt as u8 / 4).min(20);
        emit(app, "health", "running", "等待健康检查…", Some(format!("第 {} 次尝试", attempt + 1)), pct);
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    if !healthy {
        let detail = read_tail(&log_path, 4000);
        emit(app, "health", "error", "健康检查超时", Some(detail.clone()), 90);
        return Err(format!("健康检查超时，请检查日志：{}\n{detail}", log_path.display()));
    }
    emit(app, "health", "done", "健康检查通过", Some(url.clone()), 95);

    {
        let st = app.state::<DeployState>();
        *st.child.lock().unwrap() = Some(child);
        *st.data_dir.lock().unwrap() = Some(data_dir.clone());
    }
    emit(app, "finish", "done", "部署完成", Some(format!("https://127.0.0.1:{}", req.api_port)), 100);

    Ok(DeployResult {
        ok: true,
        webui_url: format!("https://127.0.0.1:{}", req.api_port),
        control_addr: format!("0.0.0.0:{}", req.control_port),
        fingerprint,
        admin_user: req.admin_user.clone(),
        generated_password: if admin_created && req.admin_password.is_empty() {
            read_admin_password(&data_dir)
        } else {
            None
        },
        data_dir: data_dir.to_string_lossy().into_owned(),
        log_file: Some(log_path.to_string_lossy().into_owned()),
    })
}

fn read_tail(path: &Path, max: usize) -> String {
    let content = std::fs::read_to_string(path).unwrap_or_default();
    if content.len() <= max {
        return content;
    }
    let start = content.len() - max;
    // Align to a char boundary to avoid panicking on multi-byte UTF-8.
    let start = (start..content.len()).find(|i| content.is_char_boundary(*i)).unwrap_or(content.len());
    content[start..].to_string()
}

fn read_admin_password(dir: &Path) -> Option<String> {
    let text = std::fs::read_to_string(dir.join("admin.txt")).ok()?;
    text.lines()
        .find_map(|l| l.strip_prefix("password: ").map(|s| s.trim().to_string()))
}

#[derive(Serialize)]
pub struct ServerStatus {
    pub running: bool,
    pub healthy: bool,
    pub data_dir: Option<String>,
    pub webui_url: Option<String>,
    pub last_error: Option<String>,
}

#[tauri::command]
pub async fn server_status(_app: AppHandle, state: State<'_, DeployState>) -> Result<ServerStatus, String> {
    let mut running = false;
    {
        let mut guard = state.child.lock().unwrap();
        if let Some(child) = guard.as_mut() {
            match child.try_wait() {
                Ok(Some(_)) => {
                    *guard = None;
                }
                Ok(None) => running = true,
                Err(_) => {}
            }
        }
    }
    let dir = state.data_dir.lock().unwrap().clone();
    let mut webui = None;
    let mut healthy = false;
    if let Some(dir) = &dir {
        if let Ok(cfg) = std::fs::read_to_string(dir.join("config.yaml")) {
            let port = cfg
                .lines()
                .find_map(|l| l.strip_prefix("api_addr:"))
                .and_then(|addr| addr.trim().rsplit(':').next().map(|s| s.to_string()));
            if let Some(port) = port {
                let url = format!("https://127.0.0.1:{port}");
                if let Ok(client) = reqwest::Client::builder().danger_accept_invalid_certs(true).timeout(Duration::from_secs(2)).build() {
                    healthy = client
                        .get(format!("{url}/api/v1/health"))
                        .send()
                        .await
                        .map(|r| r.status().is_success())
                        .unwrap_or(false);
                }
                webui = Some(url);
            }
        }
    }
    Ok(ServerStatus {
        running,
        healthy,
        data_dir: dir.map(|d| d.to_string_lossy().into_owned()),
        webui_url: webui,
        last_error: state.last_error.lock().unwrap().clone(),
    })
}

#[tauri::command]
pub async fn stop_server(app: AppHandle, state: State<'_, DeployState>) -> Result<bool, String> {
    // Take the child out of the mutex before awaiting so the future stays Send.
    let mut child = { state.child.lock().unwrap().take() };
    if let Some(child) = child.as_mut() {
        child.start_kill().map_err(|e| e.to_string())?;
        let _ = tokio::time::timeout(Duration::from_secs(10), child.wait()).await;
    }
    drop(child);
    let _ = app.emit("deploy://progress", Progress {
        step: "stop".into(),
        status: "done".into(),
        message: "服务端已停止".into(),
        detail: None,
        percent: 0,
    });
    Ok(true)
}

#[tauri::command]
pub async fn server_logs(state: State<'_, DeployState>, lines: Option<usize>) -> Result<String, String> {
    let dir = state.data_dir.lock().unwrap().clone().ok_or("尚未部署服务端")?;
    let path = dir.join("logs").join("desktop-server.log");
    let content = std::fs::read_to_string(&path).map_err(|e| format!("读取日志失败：{e}"))?;
    let n = lines.unwrap_or(200);
    let collected: Vec<&str> = content.lines().collect();
    let start = collected.len().saturating_sub(n);
    Ok(collected[start..].join("\n"))
}

#[derive(Serialize)]
pub struct InstallNotes {
    pub platform: String,
    pub firewall_hint: String,
    pub docker_alternative: String,
}

#[tauri::command]
pub fn install_notes() -> InstallNotes {
    let (firewall, docker) = if cfg!(target_os = "macos") {
        (
            "macOS 通常无需手动放行；若启用了防火墙，请在「系统设置 → 网络 → 防火墙」中允许 YukiTunnel。".to_string(),
            "也可运行：docker compose up -d（见仓库根目录 docker-compose.yml）".to_string(),
        )
    } else if cfg!(target_os = "windows") {
        (
            "首次启动时 Windows 会弹出防火墙提示，请允许专用与公用网络；也可手动放行 API、控制端口与端口范围。".to_string(),
            "也可在 PowerShell 中运行：docker compose up -d".to_string(),
        )
    } else {
        (
            "如启用了 ufw：sudo ufw allow 8443/tcp && sudo ufw allow 9443/tcp && sudo ufw allow 20000:30000/tcp".to_string(),
            "也可运行：docker compose up -d".to_string(),
        )
    };
    InstallNotes {
        platform: std::env::consts::OS.into(),
        firewall_hint: firewall,
        docker_alternative: docker,
    }
}
