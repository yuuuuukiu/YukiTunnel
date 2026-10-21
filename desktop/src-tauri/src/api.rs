//! HTTP proxy used by the webview so that self-signed server certificates work
//! and the API is not subject to CORS or webview TLS restrictions.

use std::time::Duration;

use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
pub struct ApiRequest {
    pub method: String,
    pub url: String,
    pub token: Option<String>,
    pub body: Option<String>,
    pub insecure: bool,
}

#[derive(Serialize)]
pub struct ApiResponse {
    pub status: u16,
    pub body: String,
}

/// Only http(s) URLs are proxied; anything else is rejected.
fn valid_url(url: &str) -> bool {
    url.starts_with("https://") || url.starts_with("http://")
}

#[tauri::command]
pub async fn api_request(req: ApiRequest) -> Result<ApiResponse, String> {
    if !valid_url(&req.url) {
        return Err("仅支持 http/https 地址".into());
    }
    let client = reqwest::Client::builder()
        .danger_accept_invalid_certs(req.insecure)
        .timeout(Duration::from_secs(30))
        .build()
        .map_err(|e| e.to_string())?;

    let method = reqwest::Method::from_bytes(req.method.as_bytes()).map_err(|e| e.to_string())?;
    let mut builder = client.request(method, &req.url).header("Accept", "application/json");
    if let Some(token) = &req.token {
        if !token.is_empty() {
            builder = builder.bearer_auth(token);
        }
    }
    if let Some(body) = &req.body {
        builder = builder.header("Content-Type", "application/json").body(body.clone());
    }
    let resp = builder.send().await.map_err(|e| {
        if e.is_timeout() {
            "连接服务端超时".to_string()
        } else if e.is_connect() {
            "服务端地址不可达".to_string()
        } else {
            format!("请求失败：{e}")
        }
    })?;
    let status = resp.status().as_u16();
    let body = resp.text().await.map_err(|e| e.to_string())?;
    Ok(ApiResponse { status, body })
}
