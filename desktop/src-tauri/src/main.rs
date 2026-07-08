use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    env,
    io::{BufRead, BufReader, Read},
    path::PathBuf,
    process::{Command, Stdio},
    thread,
};
use tauri::{Emitter, Window};
use uuid::Uuid;

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct DesktopManifest {
    app_name: String,
    idiom_binary: String,
    workspace: String,
    version: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct ToolInvocation {
    tool: String,
    input: Value,
    workspace: Option<String>,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct CommandInvocation {
    command: String,
    args: Option<Vec<String>>,
    workspace: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct ToolResult {
    status: i32,
    stdout: String,
    stderr: String,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct LaneRequest {
    lane: String,
    prompt: String,
    workspace: Option<String>,
    backend_profile: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct LaneHandle {
    session_id: String,
}

#[derive(Clone, Debug, Serialize)]
#[serde(rename_all = "camelCase")]
struct IdiomEvent {
    id: String,
    session_id: Option<String>,
    #[serde(rename = "type")]
    event_type: String,
    level: String,
    title: String,
    detail: Option<String>,
    timestamp: String,
    payload: Option<Value>,
}

#[tauri::command]
fn app_manifest() -> Result<DesktopManifest, String> {
    Ok(DesktopManifest {
        app_name: "Idiom Desktop".to_string(),
        idiom_binary: resolve_idiom_binary().display().to_string(),
        workspace: current_workspace()?.display().to_string(),
        version: env!("CARGO_PKG_VERSION").to_string(),
    })
}

#[tauri::command]
fn run_command(request: CommandInvocation) -> Result<ToolResult, String> {
    let command_name = strings_trimmed(&request.command);
    validate_command_name(&command_name)?;

    let args = request.args.unwrap_or_default();
    validate_command_args(&args)?;

    let mut command = Command::new(resolve_idiom_binary());
    command.arg(command_name);
    command.args(args);

    if let Some(workspace) = request.workspace.as_deref().map(str::trim).filter(|value| !value.is_empty()) {
        command.current_dir(workspace);
    }

    let output = command
        .output()
        .map_err(|err| format!("run idiom command: {err}"))?;

    Ok(ToolResult {
        status: output.status.code().unwrap_or(-1),
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
    })
}

#[tauri::command]
fn run_tool(request: ToolInvocation) -> Result<ToolResult, String> {
    validate_tool_name(&request.tool)?;

    let mut command = Command::new(resolve_idiom_binary());
    command
        .args(["tool", request.tool.as_str(), "--input"])
        .arg(request.input.to_string());

    if let Some(workspace) = request.workspace.as_deref().map(str::trim).filter(|value| !value.is_empty()) {
        command.current_dir(workspace);
    }

    let output = command
        .output()
        .map_err(|err| format!("run idiom tool: {err}"))?;

    Ok(ToolResult {
        status: output.status.code().unwrap_or(-1),
        stdout: String::from_utf8_lossy(&output.stdout).to_string(),
        stderr: String::from_utf8_lossy(&output.stderr).to_string(),
    })
}

#[tauri::command]
fn start_lane(window: Window, request: LaneRequest) -> Result<LaneHandle, String> {
    validate_lane(&request.lane)?;

    let session_id = Uuid::new_v4().to_string();
    let handle = LaneHandle {
        session_id: session_id.clone(),
    };

    thread::spawn(move || {
        if let Err(err) = run_lane_process(window.clone(), session_id.clone(), request) {
            let _ = emit_event(
                &window,
                IdiomEvent::new(
                    Some(session_id),
                    "lane_failed",
                    "error",
                    "Lane failed",
                    Some(err),
                    None,
                ),
            );
        }
    });

    Ok(handle)
}

fn run_lane_process(window: Window, session_id: String, request: LaneRequest) -> Result<(), String> {
    emit_event(
        &window,
        IdiomEvent::new(
            Some(session_id.clone()),
            "lane_started",
            "info",
            format!("{} started", request.lane),
            Some(request.prompt.clone()),
            None,
        ),
    )?;

    let mut command = Command::new(resolve_idiom_binary());
    command.args(["lane", request.lane.as_str(), "--json"]);

    if let Some(profile) = request.backend_profile.as_deref().map(str::trim).filter(|value| !value.is_empty()) {
        command.args(["--backend-profile", profile]);
    }

    command.arg(request.prompt);
    command.stdout(Stdio::piped()).stderr(Stdio::piped());

    if let Some(workspace) = request.workspace.as_deref().map(str::trim).filter(|value| !value.is_empty()) {
        command.current_dir(workspace);
    }

    let mut child = command
        .spawn()
        .map_err(|err| format!("start idiom lane: {err}"))?;

    let stdout = child.stdout.take();
    let stderr = child.stderr.take();

    let stdout_window = window.clone();
    let stdout_session_id = session_id.clone();
    let stdout_reader = stdout.map(|stdout| {
        thread::spawn(move || -> Result<(), String> {
            for line in BufReader::new(stdout).lines() {
                let line = line.map_err(|err| format!("read idiom lane output: {err}"))?;
                if line.trim().is_empty() {
                    continue;
                }

                let payload = serde_json::from_str::<Value>(&line).ok();
                emit_event(
                    &stdout_window,
                    IdiomEvent::new(
                        Some(stdout_session_id.clone()),
                        "lane_output",
                        "info",
                        "Lane event",
                        Some(line),
                        payload,
                    ),
                )?;
            }

            Ok(())
        })
    });

    let stderr_reader = stderr.map(|stderr| {
        thread::spawn(move || -> Result<String, String> {
            let mut diagnostics = String::new();
            BufReader::new(stderr)
                .read_to_string(&mut diagnostics)
                .map_err(|err| format!("read idiom lane diagnostics: {err}"))?;

            Ok(diagnostics)
        })
    });

    let status = child
        .wait()
        .map_err(|err| format!("wait for idiom lane: {err}"))?;

    if let Some(reader) = stdout_reader {
        reader
            .join()
            .map_err(|_| "stdout reader panicked".to_string())??;
    }

    let diagnostics = if let Some(reader) = stderr_reader {
        reader
            .join()
            .map_err(|_| "stderr reader panicked".to_string())??
    } else {
        String::new()
    };

    if !diagnostics.is_empty() {
        emit_event(
            &window,
            IdiomEvent::new(
                Some(session_id.clone()),
                "lane_stderr",
                "warning",
                "Lane diagnostics",
                Some(diagnostics),
                None,
            ),
        )?;
    }

    let status = status.code().unwrap_or(-1);
    let level = if status == 0 { "success" } else { "error" };
    let event_type = if status == 0 { "lane_completed" } else { "lane_exit" };

    emit_event(
        &window,
        IdiomEvent::new(
            Some(session_id),
            event_type,
            level,
            format!("Lane exited with status {status}"),
            None,
            None,
        ),
    )
}

fn emit_event(window: &Window, event: IdiomEvent) -> Result<(), String> {
    window
        .emit("idiom://lane-event", event)
        .map_err(|err| format!("emit desktop event: {err}"))
}

fn resolve_idiom_binary() -> PathBuf {
    if let Ok(path) = env::var("IDIOM_DESKTOP_BINARY") {
        let trimmed = path.trim();
        if !trimmed.is_empty() {
            return PathBuf::from(trimmed);
        }
    }

    PathBuf::from("idiom")
}

fn current_workspace() -> Result<PathBuf, String> {
    env::current_dir().map_err(|err| format!("read current directory: {err}"))
}

fn validate_lane(lane: &str) -> Result<(), String> {
    match lane {
        "plan-execute" | "implement" | "atom-spec" => Ok(()),
        _ => Err(format!("unsupported lane {lane:?}")),
    }
}

fn validate_tool_name(tool: &str) -> Result<(), String> {
    let valid = !tool.is_empty()
        && tool
            .chars()
            .all(|ch| ch.is_ascii_alphanumeric() || ch == '_' || ch == '-');

    if valid {
        Ok(())
    } else {
        Err(format!("unsupported tool name {tool:?}"))
    }
}

fn validate_command_name(command: &str) -> Result<(), String> {
    match command {
        "architect" | "artifact" | "atom" | "audit" | "backend" | "brief" | "doctor"
        | "feature-planner" | "implement" | "init" | "lane" | "local-ops" | "mcp"
        | "plan-execute" | "probe" | "product" | "product-evidence" | "profile"
        | "queue" | "ranker" | "recover" | "release" | "skills" | "tool"
        | "version" => Ok(()),
        _ => Err(format!("unsupported command {command:?}")),
    }
}

fn validate_command_args(args: &[String]) -> Result<(), String> {
    for arg in args {
        let safe = !arg.is_empty()
            && !arg.contains('\0')
            && !arg.contains('\n')
            && !arg.contains('\r');
        if !safe {
            return Err(format!("unsupported command argument {arg:?}"));
        }
    }

    Ok(())
}

fn strings_trimmed(value: &str) -> String {
    value.trim().to_string()
}

impl IdiomEvent {
    fn new(
        session_id: Option<String>,
        event_type: impl Into<String>,
        level: impl Into<String>,
        title: impl Into<String>,
        detail: Option<String>,
        payload: Option<Value>,
    ) -> Self {
        Self {
            id: Uuid::new_v4().to_string(),
            session_id,
            event_type: event_type.into(),
            level: level.into(),
            title: title.into(),
            detail,
            timestamp: unix_timestamp(),
            payload,
        }
    }
}

fn unix_timestamp() -> String {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|duration| format!("{}", duration.as_millis()))
        .unwrap_or_else(|_| "0".to_string())
}

#[cfg(test)]
mod tests {
    use super::{validate_command_args, validate_command_name, validate_lane};

    #[test]
    fn validate_command_name_accepts_allowed_commands() {
        for command in ["doctor", "backend", "queue", "tool", "version", "plan-execute"] {
            assert!(validate_command_name(command).is_ok(), "{command} should be allowed");
        }
    }

    #[test]
    fn validate_command_name_rejects_unknown_and_control_characters() {
        for command in ["unknown", "doctor\n", "doctor\r", "doctor\0"] {
            assert!(validate_command_name(command).is_err(), "{command:?} should be rejected");
        }
    }

    #[test]
    fn validate_lane_accepts_desktop_presets() {
        for lane in ["plan-execute", "implement", "atom-spec"] {
            assert!(validate_lane(lane).is_ok(), "{lane} should be allowed");
        }
    }

    #[test]
    fn validate_lane_rejects_unknown_and_control_characters() {
        for lane in ["unknown", "implement\n", "implement\r", "implement\0"] {
            assert!(validate_lane(lane).is_err(), "{lane:?} should be rejected");
        }
    }

    #[test]
    fn validate_command_args_rejects_empty_and_control_characters() {
        assert!(validate_command_args(&["--json".to_string(), "status".to_string()]).is_ok());

        for arg in ["", "--json\n", "--json\r", "--json\0"] {
            assert!(validate_command_args(&[arg.to_string()]).is_err(), "{arg:?} should be rejected");
        }
    }
}

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![app_manifest, run_command, run_tool, start_lane])
        .run(tauri::generate_context!())
        .expect("error while running Idiom Desktop");
}
