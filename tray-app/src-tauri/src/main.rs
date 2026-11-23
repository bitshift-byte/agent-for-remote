#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Mutex;
use tauri::{
    menu::{Menu, MenuItem, PredefinedMenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, RunEvent, WindowEvent,
};
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_opener::OpenerExt;
use tauri_plugin_shell::{process::CommandChild, ShellExt};

struct SidecarState(Mutex<Option<CommandChild>>);

#[tauri::command]
fn pick_folder(app: tauri::AppHandle) -> Option<String> {
    app.dialog()
        .file()
        .set_title("选择要与云端 /workspace 同步的本地仓库目录")
        .blocking_pick_folder()
        .map(|p| p.to_string())
}

#[tauri::command]
fn open_url(app: tauri::AppHandle, url: String) -> Result<(), String> {
    app.opener()
        .open_url(url, None::<&str>)
        .map_err(|e| e.to_string())
}

fn show_main_window(app: &tauri::AppHandle) {
    if let Some(win) = app.get_webview_window("main") {
        let _ = win.show();
        let _ = win.unminimize();
        let _ = win.set_focus();
    }
}

fn stop_sidecar(app: &tauri::AppHandle) {
    if let Some(state) = app.try_state::<SidecarState>() {
        if let Ok(mut guard) = state.0.lock() {
            if let Some(child) = guard.take() {
                let _ = child.kill();
            }
        }
    }
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .manage(SidecarState(Mutex::new(None)))
        .invoke_handler(tauri::generate_handler![pick_folder, open_url])
        .setup(|app| {
            // Keep macOS app cleanly in the top Menu Bar without cluttering the Dock
            #[cfg(target_os = "macos")]
            app.set_activation_policy(tauri::ActivationPolicy::Accessory);

            // Spawn bundled Go sync-agent sidecar in local control API mode
            let sidecar_cmd = app
                .shell()
                .sidecar("sync-agent")
                .expect("failed to locate sync-agent sidecar")
                .args(["-serve", "-addr", "127.0.0.1:17890"]);

            if let Ok((_rx, child)) = sidecar_cmd.spawn() {
                if let Some(state) = app.try_state::<SidecarState>() {
                    if let Ok(mut guard) = state.0.lock() {
                        *guard = Some(child);
                    }
                }
            }

            // Build native Menu Bar / System Tray menu
            let item_open = MenuItem::with_id(app, "open_panel", "打开控制台 / 切换仓库…", true, None::<&str>)?;
            let item_web = MenuItem::with_id(app, "open_web", "打开云桌面网页", true, None::<&str>)?;
            let sep = PredefinedMenuItem::separator(app)?;
            let item_quit = MenuItem::with_id(app, "quit", "退出 Super-Agent", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&item_open, &item_web, &sep, &item_quit])?;

            let mut tray_builder = TrayIconBuilder::new()
                .menu(&menu)
                .tooltip("Super-Agent 本地伴侣")
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "open_panel" => show_main_window(app),
                    "open_web" => {
                        let _ = app
                            .opener()
                            .open_url("http://localhost:8080", None::<&str>);
                    }
                    "quit" => {
                        stop_sidecar(app);
                        app.exit(0);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        show_main_window(tray.app_handle());
                    }
                });

            if let Some(icon) = app.default_window_icon() {
                tray_builder = tray_builder.icon(icon.clone());
            }
            tray_builder.build(app)?;

            Ok(())
        })
        .on_window_event(|window, event| {
            // Hide to tray/menu bar when user clicks close button instead of quitting
            if let WindowEvent::CloseRequested { api, .. } = event {
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building Super-Agent tray app");

    app.run(|app_handle, event| {
        if let RunEvent::ExitRequested { .. } | RunEvent::Exit = event {
            stop_sidecar(app_handle);
        }
    });
}
