import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import {
	createBashToolDefinition,
	VERSION,
	type ExtensionAPI,
	type McpServerConfig,
} from "@earendil-works/pi-coding-agent";

interface SessionConfig {
	shellEnv?: Record<string, string>;
	mcpServers?: { name: string; config: McpServerConfig }[];
	inject?: boolean;
}

function resolveShellPath(path?: string): string | undefined {
	if (path === "~") return homedir();
	if (path?.startsWith("~/")) return join(homedir(), path.slice(2));
	if (path?.startsWith("file://")) return fileURLToPath(path);
	return path;
}

const injectCommand = "agent-sdk-inject";

type InjectBlock =
	| { type: "text"; text: string }
	| { type: "image"; data: string; mimeType: string };

function isInjectBlock(value: unknown): value is InjectBlock {
	if (typeof value !== "object" || value === null) return false;
	if ("type" in value && value.type === "text") {
		return "text" in value && typeof value.text === "string";
	}
	return "type" in value && value.type === "image"
		&& "data" in value && typeof value.data === "string" && value.data.length > 0
		&& "mimeType" in value && typeof value.mimeType === "string" && value.mimeType.length > 0;
}

export default function sessionExtension(pi: ExtensionAPI) {
	if (VERSION !== "1.6.0") {
		throw new Error("The SDK session extension requires Pi 1.6.0");
	}
	const config: SessionConfig = JSON.parse(
		readFileSync(new URL("./session.json", import.meta.url), "utf8"),
	);
	for (const server of config.mcpServers ?? []) {
		pi.registerMcpServer(server.name, server.config);
	}
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
	pi.on("session_start", () => {
		if (config.shellEnv && !pi.getActiveTools().includes("bash")) {
			throw new Error("The SDK session extension requires an active bash tool for ShellEnv");
		}
		if (config.inject) {
			pi.registerCommand(injectCommand, {
				description: "Append model context without starting a turn",
				handler: async (args) => {
					let content: unknown;
					try {
						content = JSON.parse(args);
					} catch {
						throw new Error("SDK context injection expects a JSON block array");
					}
					if (!Array.isArray(content) || content.length === 0 || !content.every(isInjectBlock)) {
						throw new Error("SDK context injection expects text or image blocks");
					}
					pi.sendMessage({ customType: injectCommand, content, display: false }, { triggerTurn: false });
				},
			});
		}
		pi.registerCommand("agent-sdk-session-ready", {
			description: "SDK session extension handshake",
			handler: async () => {},
		});
	});
}
