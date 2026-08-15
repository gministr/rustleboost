#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod commands;
mod daemon;
mod tray;

use tauri::Manager;
use tauri_plugin_autostart::MacosLauncher;

fn main() {
    // Elevation (needed for WinTun in TUN mode) is requested by the embedded
    // manifest (see build.rs) — Windows shows the UAC prompt before this
    // process even starts, so there is nothing to check or relaunch here.

    tauri::Builder::default()
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            Some(vec!["--minimized"]),
        ))
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .invoke_handler(tauri::generate_handler![
            commands::get_status,
            commands::connect_server,
            commands::disconnect,
            commands::get_servers,
            commands::get_subscription,
            commands::update_subscription,
            commands::get_settings,
            commands::save_settings,
            commands::ping_server,
            commands::ping_all,
            commands::set_autostart,
            commands::get_daemon_port,
            commands::get_hwid,
        ])
        .setup(|app| {
            tauri::async_runtime::spawn(async {
                daemon::start_daemon().await;
            });

            if let Err(e) = tray::setup_tray(app) {
                eprintln!("[tray] setup failed: {}", e);
            }

            let args: Vec<String> = std::env::args().collect();
            if args.contains(&"--minimized".to_string()) {
                if let Some(win) = app.get_webview_window("main") {
                    let _ = win.hide();
                }
            }

            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
