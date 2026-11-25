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
}

function resolveShellPath(path?: string): string | undefined {
	if (path === "~") return homedir();
	if (path?.startsWith("~/")) return join(homedir(), path.slice(2));
	if (path?.startsWith("file://")) return fileURLToPath(path);
	return path;
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
		pi.registerCommand("agent-sdk-session-ready", {
			description: "SDK session extension handshake",
			handler: async () => {},
		});
	});
}
