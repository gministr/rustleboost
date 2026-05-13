use serde::{Deserialize, Serialize};
use tauri_plugin_autostart::ManagerExt;

use crate::daemon::get_port;

fn daemon_url(path: &str) -> String {
    format!("http://localhost:{}{}", get_port(), path)
}

async fn get_client() -> reqwest::Client {
    reqwest::Client::builder()
        .timeout(std::time::Duration::from_secs(10))
        .build()
        .unwrap()
}

#[tauri::command]
pub async fn get_status() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .get(daemon_url("/api/status"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn connect_server(server_id: String) -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/connect"))
        .json(&serde_json::json!({ "server_id": server_id }))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn disconnect() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/disconnect"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn get_servers() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .get(daemon_url("/api/servers"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn update_subscription(url: String) -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/subscription"))
        .json(&serde_json::json!({ "url": url }))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn get_settings() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .get(daemon_url("/api/settings"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn save_settings(settings: serde_json::Value) -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/settings"))
        .json(&settings)
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn ping_server(server_id: String) -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/ping"))
        .json(&serde_json::json!({ "server_id": server_id }))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn ping_all() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .post(daemon_url("/api/ping-all"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn set_autostart(
    app: tauri::AppHandle,
    enabled: bool,
) -> Result<(), String> {
    let autostart = app.autolaunch();
    if enabled {
        autostart.enable().map_err(|e| e.to_string())
    } else {
        autostart.disable().map_err(|e| e.to_string())
    }
}

#[tauri::command]
pub async fn get_hwid() -> Result<serde_json::Value, String> {
    let client = get_client().await;
    client
        .get(daemon_url("/api/hwid"))
        .send()
        .await
        .map_err(|e| e.to_string())?
        .json()
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub fn get_daemon_port() -> u16 {
    get_port()
}
