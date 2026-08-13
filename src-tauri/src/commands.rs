use tauri_plugin_autostart::ManagerExt;

use crate::daemon::get_port;

fn daemon_url(path: &str) -> String {
    format!("http://localhost:{}{}", get_port(), path)
}

fn client() -> reqwest::Client {
    reqwest::Client::builder()
        // Connecting starts two cores and waits for the proxy port, so this
        // has to outlast a slow handshake rather than time out mid-connect.
        .timeout(std::time::Duration::from_secs(90))
        .build()
        .unwrap()
}

/// Sends a request and surfaces daemon-reported failures as `Err`.
///
/// The daemon answers errors with a non-2xx status and an `{"error": ...}`
/// body. Decoding the body without checking the status would turn a failed
/// connect into a successful-looking response and leave the UI silent.
async fn send(req: reqwest::RequestBuilder) -> Result<serde_json::Value, String> {
    let resp = req.send().await.map_err(|e| e.to_string())?;
    let status = resp.status();
    let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;

    if !status.is_success() {
        let message = body
            .get("error")
            .and_then(|v| v.as_str())
            .unwrap_or("неизвестная ошибка");
        return Err(message.to_string());
    }
    Ok(body)
}

#[tauri::command]
pub async fn get_status() -> Result<serde_json::Value, String> {
    send(client().get(daemon_url("/api/status"))).await
}

#[tauri::command]
pub async fn connect_server(server_id: String) -> Result<serde_json::Value, String> {
    send(client()
        .post(daemon_url("/api/connect"))
        .json(&serde_json::json!({ "server_id": server_id })))
    .await
}

#[tauri::command]
pub async fn connect_fastest() -> Result<serde_json::Value, String> {
    send(client().post(daemon_url("/api/connect-fastest"))).await
}

#[tauri::command]
pub async fn disconnect() -> Result<serde_json::Value, String> {
    send(client().post(daemon_url("/api/disconnect"))).await
}

#[tauri::command]
pub async fn get_servers() -> Result<serde_json::Value, String> {
    send(client().get(daemon_url("/api/servers"))).await
}

#[tauri::command]
pub async fn get_subscription() -> Result<serde_json::Value, String> {
    send(client().get(daemon_url("/api/subscription"))).await
}

#[tauri::command]
pub async fn update_subscription(url: String) -> Result<serde_json::Value, String> {
    send(client()
        .post(daemon_url("/api/subscription"))
        .json(&serde_json::json!({ "url": url })))
    .await
}

#[tauri::command]
pub async fn get_settings() -> Result<serde_json::Value, String> {
    send(client().get(daemon_url("/api/settings"))).await
}

#[tauri::command]
pub async fn save_settings(settings: serde_json::Value) -> Result<serde_json::Value, String> {
    send(client().post(daemon_url("/api/settings")).json(&settings)).await
}

#[tauri::command]
pub async fn ping_server(server_id: String) -> Result<serde_json::Value, String> {
    send(client()
        .post(daemon_url("/api/ping"))
        .json(&serde_json::json!({ "server_id": server_id })))
    .await
}

#[tauri::command]
pub async fn ping_all() -> Result<serde_json::Value, String> {
    send(client().post(daemon_url("/api/ping-all"))).await
}

#[tauri::command]
pub async fn get_hwid() -> Result<serde_json::Value, String> {
    send(client().get(daemon_url("/api/hwid"))).await
}

#[tauri::command]
pub async fn set_autostart(app: tauri::AppHandle, enabled: bool) -> Result<(), String> {
    let autostart = app.autolaunch();
    if enabled {
        autostart.enable().map_err(|e| e.to_string())
    } else {
        autostart.disable().map_err(|e| e.to_string())
    }
}

#[tauri::command]
pub fn get_daemon_port() -> u16 {
    get_port()
}
