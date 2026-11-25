import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { Type } from "typebox";
import type { ExtensionContext } from "@earendil-works/pi-coding-agent";
import {
	createBashToolDefinition,
	VERSION,
	type ExtensionAPI,
} from "@earendil-works/pi-coding-agent";

interface SessionConfig {
	shellEnv?: Record<string, string>;
	toolApproval?: boolean;
	agentQuestions?: boolean;
	interactionTimeoutMs?: number;
}

function resolveShellPath(path?: string): string | undefined {
	if (path === "~") return homedir();
	if (path?.startsWith("~/")) return join(homedir(), path.slice(2));
	if (path?.startsWith("file://")) return fileURLToPath(path);
	return path;
}

const approvalTools = new Set(["write", "edit", "bash", "powershell"]);

async function dialog<T>(
	timeout: number,
	signal: AbortSignal | undefined,
	ask: (options: { timeout: number; signal: AbortSignal }) => Promise<T>,
) {
	const expiry = new AbortController();
	const combined = signal ? AbortSignal.any([signal, expiry.signal]) : expiry.signal;
	const timer = setTimeout(() => expiry.abort(), timeout);
	try {
		const answer = await ask({ timeout, signal: combined });
		return { answer, expired: expiry.signal.aborted };
	} finally {
		clearTimeout(timer);
	}
}

function registerToolApproval(pi: ExtensionAPI, timeout: number) {
	pi.on("tool_call", async (event, ctx) => {
		if (!approvalTools.has(event.toolName)) return;
		const input = event.input as Record<string, unknown>;
		const detail = input.command ?? input.path ?? "";
		const message = JSON.stringify({ tool: event.toolName, toolCallId: event.toolCallId, detail });
		const { answer, expired } = await dialog(timeout, ctx.signal, (opts) =>
			ctx.ui.confirm("Agent SDK tool approval", message, opts),
		);
		if (!answer) {
			return {
				block: true,
				reason: expired ? "Agent SDK tool approval expired" : "Agent SDK tool approval denied",
			};
		}
	});
}

function registerQuestionTool(pi: ExtensionAPI, timeout: number) {
	pi.registerTool({
		name: "agent_sdk_ask",
		label: "Ask user",
		description: "Ask the user one question. Supply choices for single selection or omit them for free text.",
		parameters: Type.Object({
			question: Type.String({ minLength: 1 }),
			choices: Type.Optional(Type.Array(Type.String({ minLength: 1 }), { minItems: 2 })),
		}),
		async execute(toolCallId, { question, choices }, signal, _onUpdate, ctx: ExtensionContext) {
			if (choices && new Set(choices).size !== choices.length) {
				throw new Error("Question choices must be distinct");
			}
			const title = "Agent SDK question: " + JSON.stringify({ question, toolCallId });
			const { answer, expired } = await dialog(timeout, signal, (opts) =>
				choices ? ctx.ui.select(title, choices, opts) : ctx.ui.input(title, undefined, opts),
			);
			return {
				content: [{ type: "text", text: answer ?? "The user did not answer." }],
				details: { sdkInteractionExpired: expired },
			};
		},
	});
}

export default function sessionExtension(pi: ExtensionAPI) {
	if (VERSION !== "1.6.0") {
		throw new Error("The SDK session extension requires Pi 1.6.0");
	}
	const config: SessionConfig = JSON.parse(
		readFileSync(new URL("./session.json", import.meta.url), "utf8"),
	);
	if (config.shellEnv) {
		const shellEnv = config.shellEnv;
		const bash = createBashToolDefinition(process.cwd());
		pi.registerTool({
			...bash,
			execute(...args) {
				const settings = pi.getSettings();
				return createBashToolDefinition(process.cwd(), {
					commandPrefix: settings.shellCommandPrefix,
					shellPath: resolveShellPath(settings.shellPath),
					spawnHook: (context) => ({ ...context, env: { ...context.env, ...shellEnv } }),
				}).execute(...args);
			},
		});
	}
	const timeout = config.interactionTimeoutMs ?? 60_000;
	if (config.toolApproval) registerToolApproval(pi, timeout);
	if (config.agentQuestions) registerQuestionTool(pi, timeout);
	pi.on("session_start", () => {
		const active = pi.getActiveTools();
		if (config.shellEnv && !active.includes("bash")) {
			throw new Error("The SDK session extension requires an active bash tool for ShellEnv");
		}
		if (config.toolApproval && !active.some((tool) => approvalTools.has(tool))) {
			throw new Error("The SDK session extension requires active local tools for approvals");
		}
		if (config.agentQuestions && !active.includes("agent_sdk_ask")) {
			throw new Error("The SDK session extension could not activate its question tool");
		}
		pi.registerCommand("agent-sdk-session-ready", {
			description: "SDK session extension handshake",
			handler: async () => {},
		});
	});
}
