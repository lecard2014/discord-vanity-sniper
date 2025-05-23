"use strict";
process.env["NODE_TLS_REJECT_UNAUTHORIZED"] = "0";

const tls = require("tls");
const WebSocket = require("ws");
const fs = require("fs/promises");
const extractJsonFromString = require("extract-json-from-string");
const axios = require("axios");

let vanity, websocket, mfaToken;

const token = "tokenburaya";
const targetGuildId = "sunucu id";

const guilds = {};

const requestTimings = new Map();
const vanityRequestCache = new Map();

const CONNECTION_POOL_SIZE = 3;
const tlsConnections = [];

function getVanityPatchRequestBuffer(vanityCode) {
    if (vanityRequestCache.has(vanityCode)) {
        return vanityRequestCache.get(vanityCode);
    }
    
    const payload = JSON.stringify({ code: vanityCode });
    const payloadLength = Buffer.byteLength(payload);
    const requestBuffer = Buffer.from(
        `PATCH /api/v6/guilds/${targetGuildId}/vanity-url HTTP/1.1\r\n` +
        `Host: canary.discord.com\r\n` +
        `Authorization: ${token}\r\n` +
        `X-Discord-MFA-Authorization: ${mfaToken}\r\n` +
        `User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x32) "+"AppleWebKit/537.36 (KHTML, like Gecko) ravixd/1.0.9164 "+ "Chrome/124.0.6367.243 Electron/30.2.0 Safari/537.36\r\n` +
        `X-Super-Properties: eyJvcyI6IldpbmRvd3MiLCJicm93c2VyIjoiRGlzY29yZCBDbGllbnQiLCJyZWxlYXNlX2NoYW5uZWwiOiJzdGFibGUiLCJjbGllbnRfdmVyc2lvbiI6IjEuMC45MTY0Iiwib3NfdmVyc2lvbiI6IjEwLjAuMjI2MzEiLCJvc19hcmNoIjoieDY0IiwiYXBwX2FyY2giOiJ4NjQiLCJzeXN0ZW1fbG9jYWxlIjoidHIiLCJicm93c2VyX3VzZXJfYWdlbnQiOiJNb3ppbGxhLzUuMCAoV2luZG93cyBOVCAxMC4wOyBXaW42NDsgeDY0KSBBcHBsZVdlYktpdC81MzcuMzYgKEtIVE1MLCBsaWtlIEdlY2tvKSBkaXNjb3JkLzEuMC45MTY0IENocm9tZS8xMjQuMC42MzY3LjI0MyBFbGVjdHJvbi8zMC4yLjAgU2FmYXJpLzUzNy4zNiIsImJyb3dzZXJfdmVyc2lvbiI6IjMwLjIuMCIsIm9zX3Nka192ZXJzaW9uIjoiMjI2MzEiLCJjbGllbnRfdnVibF9udW1iZXIiOjUyODI2LCJjbGllbnRfZXZlbnRfc291cmNlIjpudWxsfQ==\r\n` +
        `Content-Type: application/json\r\n` +
        `Connection: close\r\n` +
        `Content-Length: ${payloadLength}\r\n\r\n` +
        payload
    );
    
    vanityRequestCache.set(vanityCode, requestBuffer);
    return requestBuffer;
}

function sendWebhookNotification(vanityCode) {
	const webhookUrl = "https://discord.com/api/webhooks/1316477015385178123/AAYf0cRBUG1UiZPnpLy_X3nI5_5sOrNQdjgr-olJz_dqWEkOTX4ERPSz3paz5sLwK844";
	const payload = {
		content: "||@everyone||",
		embeds: [
			{
				title: "@r66i in ciraklarina selam olsun",
				color: 0xffffff, 
				fields: [
					{
						name: "vanity",
						value: `**\`\`\`\n${vanityCode}\n\`\`\`**`, 
						inline: false
					},
				],
				footer: {
					text: "ravi",
				},
			}
		]
	};
	
	return axios.post(webhookUrl, payload).catch(() => {});
}

const keepAliveRequest = Buffer.from(`GET / HTTP/1.1\r\nHost: canary.discord.com\r\nConnection: keep-alive\r\n\r\n`);

setInterval(() => {
    for (const conn of tlsConnections) {
        if (conn.writable) conn.write(keepAliveRequest);
    }
}, 2000);

function connectWebSocket() {
	
    websocket = new WebSocket("wss://gateway-us-east1-b.discord.gg", { perMessageDeflate: false, });
    
    websocket.onclose = () => { setTimeout(connectWebSocket, 1000); };
    
    websocket.onerror = (error) => { console.error("WebSocket error:", error); };
    
    websocket.onmessage = async (message) => {
        const { d, op, t } = JSON.parse(message.data);
        
        if (t === "READY") {
            if (d.guilds) {
                for (const g of d.guilds) {
                    if (g.vanity_url_code) guilds[g.id] = g.vanity_url_code;
                }
				console.log(Object.values(guilds));
            }
        }
        
        if (t === "GUILD_UPDATE" && d && guilds[d.guild_id] && guilds[d.guild_id] !== d.vanity_url_code) {
            const find = guilds[d.guild_id];
            vanity = find;
            const requestBuffer = getVanityPatchRequestBuffer(find);
            const requestPromises = tlsConnections.map(conn => {
                if (conn.writable) {
                    return new Promise(resolve => {
                        if (conn.setPriority) {
                            conn.setPriority(6);
                        }
                        process.nextTick(() => {
                            conn.write(requestBuffer, resolve);
                        });
                    });
                }
                return Promise.resolve();
            });
            Promise.all(requestPromises).catch(() => {});
            setTimeout(() => sendWebhookNotification(find), 50);
        }
        
        if (op === 10) {
            websocket.send(JSON.stringify({
                op: 2,
                d: {
                    token: token,
                    intents: 513 << 0,
                    properties: {
                        os: "Windows",
                        browser: "Chrome",
                        device: "Desktop",
                    },
                },
            }));
            setInterval(() => websocket.send(JSON.stringify({ op: 1, d: {}, s: null, t: "heartbeat" })), d.heartbeat_interval);
        }
    };
}

function initConnectionPool() {
    for (let i = 0; i < CONNECTION_POOL_SIZE; i++) {
        createTlsConnection();
    }
}

function createTlsConnection() {
    const tlsOptions = {
        host: "canary.discord.com",
        port: 443,
        minVersion: "TLSv1.3",
        maxVersion: "TLSv1.3",
        rejectUnauthorized: false
    };

    const connection = tls.connect(tlsOptions);

    if (connection.setPriority) { connection.setPriority(6); }

    connection.setNoDelay(true);
    
    if (connection.socket && connection.socket.setNoDelay) {
        connection.socket.setNoDelay(true);
    }

    connection.on("error", (err) => { 
        const idx = tlsConnections.indexOf(connection);
        if (idx !== -1) tlsConnections.splice(idx, 1);
        createTlsConnection();
    });

    connection.on("end", () => { 
        const idx = tlsConnections.indexOf(connection);
        if (idx !== -1) tlsConnections.splice(idx, 1);
        createTlsConnection();
    });

    connection.on("secureConnect", () => { 
        if (!tlsConnections.includes(connection)) tlsConnections.push(connection); 
    });
    
    connection.on("data", async (data) => {
        const dataStr = data.toString();
        const ext = extractJsonFromString(dataStr);
        const find = ext.find((e) => e.code) || ext.find((e) => e.message);
        if (find) {
            console.log(find);
        }
    });
    
    return connection;
}

setInterval(() => {
    requestTimings.forEach((timing, id) => {
        if (performance.now() - timing.startTime > 30000) {
            requestTimings.delete(id);
        }
    });
}, 10000);

function refreshVanityCache() {
    vanityRequestCache.clear();
}

async function readMfaToken() { 
    const newToken = await fs.readFile('mfa.txt', 'utf8');
    if (mfaToken !== newToken) {
        mfaToken = newToken;
        refreshVanityCache(); 
    }
}

async function initialize() {
    await readMfaToken(); 
    initConnectionPool();
    connectWebSocket();
    setInterval(readMfaToken, 10000); 
}

initialize();
