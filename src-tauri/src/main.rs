#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod commands;
mod daemon;
mod tray;

use tauri::Manager;
use tauri_plugin_autostart::MacosLauncher;

fn main() {
    // On Windows: request elevation for WinTun (TUN mode requires admin).
    // If not running as admin, relaunch with UAC prompt and exit.
    #[cfg(windows)]
    if !is_elevated() {
        relaunch_as_admin();
        return;
    }

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
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
        .plugin(tauri_plugin_notification::init())
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

/// Returns true if the current process has administrator privileges.
#[cfg(windows)]
fn is_elevated() -> bool {
    // SAM hive is readable only by admins
    std::fs::metadata(r"C:\Windows\System32\config\SAM").is_ok()
}

/// Re-launches the executable with administrator rights via UAC.
#[cfg(windows)]
fn relaunch_as_admin() {
    let exe = match std::env::current_exe() {
        Ok(p) => p,
        Err(_) => return,
    };

    // Pass original arguments through
    let args: Vec<String> = std::env::args().skip(1).collect();
    let args_str = if args.is_empty() {
        String::new()
    } else {
        format!(" {}", args.join(" "))
    };

    let cmd = format!(
        "Start-Process '{}'{} -Verb RunAs",
        exe.display(),
        if args_str.is_empty() { String::new() } else { format!(" -ArgumentList '{}'", args_str) }
    );

    let _ = std::process::Command::new("powershell")
        .args(["-WindowStyle", "Hidden", "-Command", &cmd])
        .spawn();
}
