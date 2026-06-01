// Experimental Tauri shell, kept alongside the Electron one. It mirrors that
// approach: spawn the emission Go binary on a free port, wait for it to listen,
// then open a window pointing at its localhost UI. The Go process serves the
// embedded React UI and the API, so the webview is just a thin client.
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;
use std::process::{Child, Command};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

// Holds the spawned server so it can be killed when the window closes.
struct Server(Mutex<Option<Child>>);

// Ask the OS for a free TCP port by binding to :0 and reading it back.
fn free_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind free port")
        .local_addr()
        .expect("read local addr")
        .port()
}

// Resolve the emission binary. In a packaged build it is the code-signed
// bundle resource. In dev (debug) we deliberately skip the resource copy —
// Tauri does not sign it, so macOS SIGKILLs it on exec — and use the repo-root
// binary, which `make tauri-run` ad-hoc signs.
fn binary_path(app: &tauri::App) -> PathBuf {
    let name = if cfg!(windows) { "emission.exe" } else { "emission" };
    if !cfg!(debug_assertions) {
        if let Ok(res) = app.path().resource_dir() {
            for cand in [res.join("bin").join(name), res.join(name)] {
                if cand.exists() {
                    return cand;
                }
            }
        }
    }
    PathBuf::from("..").join(name)
}

fn main() {
    tauri::Builder::default()
        .manage(Server(Mutex::new(None)))
        .setup(|app| {
            let port = free_port();
            // Keep all state under the per-app data dir; emission creates it.
            let torrents = app
                .path()
                .app_data_dir()
                .expect("resolve app data dir")
                .join("torrents");
            let bin = binary_path(app);
            eprintln!(
                "[emission] launching {} (port {port}, storage {})",
                bin.display(),
                torrents.display()
            );
            let mut child = Command::new(&bin)
                .args([
                    "serve",
                    "--http.ui",
                    "--http.port",
                    &port.to_string(),
                    "--storage.torrents",
                    &torrents.to_string_lossy(),
                ])
                .spawn()
                .unwrap_or_else(|e| panic!("failed to spawn {}: {e}", bin.display()));

            // Wait for the server to accept connections. If the child exits
            // first, surface its status instead of a blind timeout.
            let deadline = Instant::now() + Duration::from_secs(20);
            loop {
                if let Ok(Some(status)) = child.try_wait() {
                    panic!("emission exited before serving (status {status})");
                }
                if TcpStream::connect(("127.0.0.1", port)).is_ok() {
                    break;
                }
                if Instant::now() > deadline {
                    panic!("emission did not start in time (port {port})");
                }
                std::thread::sleep(Duration::from_millis(200));
            }

            app.state::<Server>().0.lock().unwrap().replace(child);

            let url = format!("http://localhost:{port}");
            // Match the UI's content width (Tailwind max-w-3xl = 48rem = 768px);
            // the layout's own px-4 becomes the window's edge padding.
            WebviewWindowBuilder::new(app, "main", WebviewUrl::External(url.parse().unwrap()))
                .title("Emission")
                .inner_size(768.0, 800.0)
                .build()?;
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                if let Some(mut child) =
                    window.app_handle().state::<Server>().0.lock().unwrap().take()
                {
                    let _ = child.kill();
                }
            }
        })
        .run(tauri::generate_context!())
        .expect("run tauri application");
}
