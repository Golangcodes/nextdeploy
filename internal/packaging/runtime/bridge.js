const http = require('http');
const path = require('path');
const { spawn } = require('child_process');

let serverReady = false;
let serverPort = 3000;

const serverPath = path.join(__dirname, 'server.js');

// Mode signal from the Go deploy side.
// - "env": secrets were baked into Lambda env vars (insecure opt-in fallback).
//   Skip the extension entirely; secrets are already in process.env.
// - unset / anything else: secrets live in Secrets Manager behind the AWS
//   Parameters and Secrets Lambda Extension at localhost:2773. bridge must
//   fetch and fail hard if it comes back empty.
const secretsMode = process.env.ND_SECRETS_MODE || '';
const secretName = process.env.ND_SECRET_NAME || '';

const fetchSecrets = async () => {
    if (!secretName) return {};
    if (secretsMode === 'env') {
        console.log('[bridge] ND_SECRETS_MODE=env — secrets already in process.env, skipping extension fetch');
        return {};
    }

    const options = {
        hostname: 'localhost',
        port: 2773,
        path: '/secretsmanager/get?secretId=' + encodeURIComponent(secretName),
        headers: { 'X-Aws-Parameters-Secrets-Token': process.env.AWS_SESSION_TOKEN || '' },
        timeout: 1000
    };

    console.log('[bridge] Attempting to fetch secrets from AWS Extension...');
    return new Promise((resolve) => {
        const req = http.get(options, (res) => {
            let data = '';
            res.on('data', (chunk) => data += chunk);
            res.on('end', () => {
                // Two JSON.parse calls, two distinct failure modes. Tagging each
                // so CloudWatch shows which layer broke. Never log `data` or the
                // SecretString contents themselves — they're the secrets.
                let envelope;
                try {
                    envelope = JSON.parse(data);
                } catch (e) {
                    console.error('[bridge] Extension response was not valid JSON (secret=' + secretName + '): ' + e.message);
                    return resolve({});
                }
                if (!envelope.SecretString) {
                    console.error('[bridge] Extension returned envelope without SecretString (secret=' + secretName + ')');
                    return resolve({});
                }
                try {
                    const secrets = JSON.parse(envelope.SecretString);
                    console.log('[bridge] Successfully fetched secrets from extension');
                    resolve(secrets);
                } catch (e) {
                    console.error('[bridge] SecretString is not valid JSON (secret=' + secretName + '): ' + e.message + ' — check that the blob stored in Secrets Manager is a JSON object of key/value pairs');
                    resolve({});
                }
            });
        });
        req.on('error', (err) => {
            console.log('[bridge] Secrets Extension unreachable:', err.message);
            resolve({});
        });
        req.on('timeout', () => {
            req.destroy();
            console.log('[bridge] Secrets Extension request timed out');
            resolve({});
        });
        req.end();
    });
};

const waitForServer = async () => {
    for (let i = 0; i < 50; i++) {
        try {
            await new Promise((resolve, reject) => {
                const req = http.get({
                    hostname: '127.0.0.1',
                    port: serverPort,
                    path: '/',
                    timeout: 500,
                }, (res) => resolve(true));
                req.on('error', reject);
                req.end();
            });
            return true;
        } catch (e) { await new Promise(r => setTimeout(r, 100)); }
    }
    throw new Error('Server timed out');
};

const startServer = async () => {
    const secrets = await fetchSecrets();

    // FATAL guard: in extension mode the app MUST receive secrets. Booting
    // Next.js with an empty secret map guarantees a 500 on the first DB/API
    // call with a stack trace that won't point at the root cause. Exiting
    // here surfaces the real issue (IAM, missing blob, malformed JSON) in
    // CloudWatch and makes Lambda retry the cold start cleanly.
    if (secretName && secretsMode !== 'env' && Object.keys(secrets).length === 0) {
        console.error('[bridge] FATAL: ND_SECRET_NAME=' + secretName + ' is set but no secrets were returned. Refusing to start server. Check the Secrets Extension layer, IAM (secretsmanager:GetSecretValue), and that the secret blob is non-empty JSON.');
        process.exit(1);
    }

    const env = { ...process.env, ...secrets, PORT: String(serverPort), HOSTNAME: '127.0.0.1', NODE_ENV: 'production' };
    const serverProcess = spawn('node', [serverPath], { env: env, stdio: 'inherit' });
    serverProcess.on('exit', () => { serverReady = false; });
    await waitForServer();
    serverReady = true;
};

// ND_BRIDGE_NO_WARMUP=1 short-circuits the child-process spawn so the module
// can be required in unit tests without starting Next.js. Lambda never sets
// this — bridge.js is the runtime entry point there.
const warmup = process.env.ND_BRIDGE_NO_WARMUP === '1' ? Promise.resolve() : startServer();

// Lazy-init SQS client: only constructed on the first revalidation request.
// Keeps cold-start cost zero for request-path invocations and lets us skip the
// SDK import entirely in environments where the queue isn't configured.
let sqsClient = null;
let SendMessageCommand = null;
const getSQS = () => {
    if (sqsClient) return sqsClient;
    const sdk = require('@aws-sdk/client-sqs');
    SendMessageCommand = sdk.SendMessageCommand;
    sqsClient = new sdk.SQSClient({});
    return sqsClient;
};

// handleRevalidate enqueues {tag?, path?} to the ISR revalidation queue.
// The auxiliary revalidator Lambda consumes the queue and calls
// CloudFront CreateInvalidation. Without this endpoint, Next.js
// revalidatePath/revalidateTag only invalidates in-process caches — the CDN
// keeps serving stale HTML until the distribution's TTL expires.
const handleRevalidate = async (event) => {
    const queueUrl = process.env.ND_REVALIDATION_QUEUE;
    if (!queueUrl) {
        return {
            statusCode: 503,
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ error: 'revalidation queue not configured (ND_REVALIDATION_QUEUE unset)' }),
        };
    }

    let payload;
    try {
        const raw = event.isBase64Encoded && event.body
            ? Buffer.from(event.body, 'base64').toString('utf8')
            : (event.body || '{}');
        payload = JSON.parse(raw);
    } catch (e) {
        return {
            statusCode: 400,
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ error: 'invalid JSON body: ' + e.message }),
        };
    }

    if (!payload.tag && !payload.path) {
        return {
            statusCode: 400,
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ error: 'body must include "tag" or "path"' }),
        };
    }

    try {
        const client = getSQS();
        await client.send(new SendMessageCommand({
            QueueUrl: queueUrl,
            MessageBody: JSON.stringify(payload),
        }));
        return {
            statusCode: 202,
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ enqueued: true }),
        };
    } catch (e) {
        console.error('[bridge] SQS SendMessage failed:', e.message);
        return {
            statusCode: 502,
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({ error: 'failed to enqueue revalidation: ' + e.message }),
        };
    }
};

// Internal surface for unit tests. Consumers outside bridge.test.js must not
// rely on this shape — it's an implementation detail.
exports.__test__ = { handleRevalidate, fetchSecrets };

exports.handler = async (event) => {
    await warmup;
    const method = (event.requestContext && event.requestContext.http) ? event.requestContext.http.method : event.httpMethod;
    const rawPath = event.rawPath || event.path || '/';

    if (rawPath === '/_nextdeploy/revalidate' && method === 'POST') {
        return handleRevalidate(event);
    }

    return new Promise((resolve, reject) => {
        const queryString = event.rawQueryString || '';

        const incomingHeaders = event.headers || {};
        const getHeader = (name) => {
            const lowerName = name.toLowerCase();
            for (const key in incomingHeaders) {
                if (key.toLowerCase() === lowerName) return incomingHeaders[key];
            }
            return null;
        };

        // Recover original Host for Server Action CSRF bypass
        const cfDomain = process.env.ND_CF_DOMAIN;
        const customDomain = process.env.ND_CUSTOM_DOMAIN;
        const origin = getHeader('origin');
        const incomingProto = getHeader('x-forwarded-proto') || 'https';
        let fwHost = getHeader('x-forwarded-host') || getHeader('host') || 'localhost';

        if (origin) {
            fwHost = origin.replace(/^https?:\/\//, '');
        } else if (customDomain) {
            fwHost = customDomain;
        } else if (cfDomain) {
            fwHost = cfDomain;
        }

        const headers = {
            ...incomingHeaders,
            'x-forwarded-proto': incomingProto,
            'x-forwarded-port': '3000',
            'x-forwarded-host': fwHost,
            'host': fwHost,
        };

        const options = {
            hostname: '127.0.0.1',
            port: serverPort,
            path: rawPath + (queryString ? '?' + queryString : ''),
            method: method,
            headers: headers,
        };
        const req = http.request(options, (res) => {
            const chunks = [];
            res.on('data', (chunk) => chunks.push(chunk));
            res.on('end', () => {
                const body = Buffer.concat(chunks);
                resolve({
                    statusCode: res.statusCode,
                    headers: res.headers,
                    body: body.toString('base64'),
                    isBase64Encoded: true
                });
            });
        });
        if (event.body) {
            req.write(event.isBase64Encoded ? Buffer.from(event.body, 'base64') : event.body);
        }
        req.on('error', reject);
        req.end();
    });
};
