use std::sync::atomic::{AtomicU16, Ordering};
use std::time::Duration;

static DAEMON_PORT: AtomicU16 = AtomicU16::new(7777);

pub fn get_port() -> u16 {
    DAEMON_PORT.load(Ordering::Relaxed)
}

pub async fn start_daemon() {
    // Kill any leftover daemon processes from previous runs
    kill_old_daemons();

    let port = find_free_port().unwrap_or(7777);
    DAEMON_PORT.store(port, Ordering::Relaxed);

    let daemon_path = match find_daemon_binary() {
        Some(p) => p,
        None => {
            eprintln!("[daemon] binary not found next to executable");
            return;
        }
    };

    eprintln!("[daemon] launching {:?} on port {}", daemon_path, port);

    let mut cmd = std::process::Command::new(&daemon_path);
    cmd.args(["--port", &port.to_string()]);

    // Suppress console window on Windows
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x08000000); // CREATE_NO_WINDOW
    }

    match cmd.spawn() {
        Ok(_child) => eprintln!("[daemon] spawned OK"),
        Err(e) => {
            eprintln!("[daemon] spawn failed: {}", e);
            return;
        }
    }

    wait_for_daemon(port).await;
}

fn find_daemon_binary() -> Option<std::path::PathBuf> {
    let exe_dir = std::env::current_exe().ok()?.parent()?.to_path_buf();

    #[cfg(windows)]
    let name = "daemon.exe";
    #[cfg(not(windows))]
    let name = "daemon";

    let candidate = exe_dir.join(name);
    if candidate.exists() {
        return Some(candidate);
    }

    // Dev fallback: look in src-tauri/binaries/
    let dev_candidate = exe_dir
        .ancestors()
        .find(|p| p.join("src-tauri").exists())
        .map(|p| {
            p.join("src-tauri")
                .join("binaries")
                .join(format!("daemon-x86_64-pc-windows-msvc.exe"))
        });

    if let Some(p) = dev_candidate {
        if p.exists() {
            return Some(p);
        }
    }

    None
}

async fn wait_for_daemon(port: u16) {
    let url = format!("http://localhost:{}/api/status", port);
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .build()
        .unwrap_or_default();

    for i in 0..30 {
        tokio::time::sleep(Duration::from_millis(300)).await;
        if client.get(&url).send().await.is_ok() {
            eprintln!("[daemon] ready on port {} (attempt {})", port, i + 1);
            return;
        }
    }
    eprintln!("[daemon] warning: not responding after 9s");
}

fn kill_old_daemons() {
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        let _ = std::process::Command::new("taskkill")
            .args(["/F", "/IM", "daemon.exe"])
            .creation_flags(0x08000000)
            .spawn();
        std::thread::sleep(std::time::Duration::from_millis(400));
    }
    #[cfg(not(windows))]
    {
        let _ = std::process::Command::new("pkill").arg("daemon").spawn();
        std::thread::sleep(std::time::Duration::from_millis(300));
    }
}

fn find_free_port() -> Option<u16> {
    use std::net::TcpListener;
    TcpListener::bind("127.0.0.1:7777")
        .map(|l| l.local_addr().unwrap().port())
        .or_else(|_| {
            TcpListener::bind("127.0.0.1:0").map(|l| l.local_addr().unwrap().port())
        })
        .ok()
}
