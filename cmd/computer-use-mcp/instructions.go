package main

import "strings"

func computerUseInstructions() string {
	return strings.Join([]string{
		"Computer Use tools let you interact with desktop apps by performing UI actions.",
		"",
		"Some apps might have a separate dedicated plugin or skill. You may want to use that plugin or skill instead of Computer Use when it seems like a good fit for the task. While the separate plugin or skill may not expose every feature in the app, if the plugin can perform the task with its available features, prefer it. If the needed capability is not exposed there, use Computer Use may be appropriate for the missing interaction.",
		"",
		"Begin by calling `get_app_state` every turn you want to use Computer Use to get the latest state before acting. Codex will automatically stop the session after each assistant turn, so this step is required before interacting with apps in a new assistant turn.",
		"`get_app_state` supports capture_mode values `som` (screenshot plus accessibility tree), `ax` (accessibility tree without screenshot), and `vision` (screenshot/window/app state without returning the accessibility tree).",
		"Pass the returned `state_id` to every action tool. If an action reports `requires_refresh`, call `get_app_state` again and retry against the fresh state.",
		"",
		"The available tools are list_apps, get_app_state, set_recording, replay_trajectory, click, perform_secondary_action, scroll, drag, type_text, press_key, set_value, evaluate_javascript, and evaluate_cdp_javascript. If any of these are not available in your environment, use tool_search to surface one before calling any Computer Use action tools.",
		"",
		"Computer Use tools prefer background-safe AX and pid-routed actions where possible, but some fallbacks can affect the foreground session. Avoid disrupting the user's active session, such as overwriting the contents of their clipboard, unless they asked you to!",
		"",
		"The physical-user intervention monitor is disabled by default. If the server is started with --human-intervention-monitor or COMPUTER_USE_MCP_HUMAN_INTERVENTION_MONITOR=1, recent physical mouse or keyboard input pauses action tools and requires a fresh get_app_state before continuing.",
		"Set COMPUTER_USE_MCP_BLOCKED_DOMAINS to a comma-separated list of domains to block action tools on matching browser URLs.",
		"Use set_recording to capture a trajectory of subsequent action tools. Use replay_trajectory to re-run the recorded actions against fresh app state, or with dry_run to inspect the captured steps.",
		"",
		"After each action, use the action result or fetch the latest state to verify the UI changed as expected.",
		"Prefer element-targeted interactions over coordinate clicks when an index for the targeted element is available. Note that element indices are the sequential integers from the app state's accessibility tree.",
		"Use click with foreground_hid only for opaque canvas, WebGL, Metal, or game-like viewports that reject background PID-routed events. It activates the app and can disrupt the user's foreground session.",
		"Prefer type_text with element_index when a text target is available; omit element_index only when you intentionally want to type into the app's currently focused element.",
		"Prefer Computer Use tools as much as possible to complete tasks. Use evaluate_javascript only as a browser fallback when the accessibility tree cannot expose or operate the needed page state. Use evaluate_cdp_javascript for local Electron or Chromium DevTools targets when Apple Events are unavailable.",
		"Ask the user before taking destructive or externally visible actions such as sending, deleting, or purchasing. If helpful, you can ask follow-up questions before taking action to make sure you’re understanding the user’s request correctly.",
	}, "\n")
}
