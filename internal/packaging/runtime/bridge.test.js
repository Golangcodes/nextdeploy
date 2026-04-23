// Smoke tests for bridge.js. Run with: node --test bridge.test.js
//
// bridge.js spawns `node server.js` on module load. We set ND_BRIDGE_NO_WARMUP
// before requiring so the spawn is skipped. @aws-sdk/client-sqs is pre-installed
// in the Node 20 Lambda runtime but not in local dev, so we stub it via a
// require hook before the lazy-load inside getSQS() resolves it.

const { test } = require('node:test');
const assert = require('node:assert/strict');
const Module = require('node:module');

process.env.ND_BRIDGE_NO_WARMUP = '1';

let lastSendArgs = null;
let sendBehavior = 'ok'; // 'ok' | 'throw'

const origRequire = Module.prototype.require;
Module.prototype.require = function stubbed(id) {
    if (id === '@aws-sdk/client-sqs') {
        return {
            SQSClient: class {
                async send(cmd) {
                    lastSendArgs = cmd.__input;
                    if (sendBehavior === 'throw') throw new Error('simulated SQS failure');
                    return { MessageId: 'stub-msg-id' };
                }
            },
            SendMessageCommand: class {
                constructor(input) { this.__input = input; }
            },
        };
    }
    return origRequire.apply(this, arguments);
};

const bridge = require('./bridge.js');
const { handleRevalidate } = bridge.__test__;

const jsonEvent = (body, { base64 = false } = {}) => ({
    requestContext: { http: { method: 'POST' } },
    rawPath: '/_nextdeploy/revalidate',
    headers: { 'content-type': 'application/json' },
    body: base64 ? Buffer.from(body).toString('base64') : body,
    isBase64Encoded: base64,
});

test('handleRevalidate returns 503 when ND_REVALIDATION_QUEUE is unset', async () => {
    delete process.env.ND_REVALIDATION_QUEUE;
    const res = await handleRevalidate(jsonEvent(JSON.stringify({ tag: 'posts' })));
    assert.equal(res.statusCode, 503);
    assert.match(JSON.parse(res.body).error, /ND_REVALIDATION_QUEUE/);
});

test('handleRevalidate returns 400 on invalid JSON body', async () => {
    process.env.ND_REVALIDATION_QUEUE = 'https://sqs.us-east-1.amazonaws.com/123/q';
    const res = await handleRevalidate(jsonEvent('{not-json'));
    assert.equal(res.statusCode, 400);
    assert.match(JSON.parse(res.body).error, /invalid JSON/);
});

test('handleRevalidate returns 400 when body lacks both tag and path', async () => {
    process.env.ND_REVALIDATION_QUEUE = 'https://sqs.us-east-1.amazonaws.com/123/q';
    const res = await handleRevalidate(jsonEvent(JSON.stringify({ foo: 'bar' })));
    assert.equal(res.statusCode, 400);
    assert.match(JSON.parse(res.body).error, /tag.*path|path.*tag/);
});

test('handleRevalidate enqueues {tag} payload and returns 202', async () => {
    process.env.ND_REVALIDATION_QUEUE = 'https://sqs.us-east-1.amazonaws.com/123/q';
    lastSendArgs = null;
    sendBehavior = 'ok';
    const res = await handleRevalidate(jsonEvent(JSON.stringify({ tag: 'posts' })));
    assert.equal(res.statusCode, 202);
    assert.equal(JSON.parse(res.body).enqueued, true);
    assert.equal(lastSendArgs.QueueUrl, 'https://sqs.us-east-1.amazonaws.com/123/q');
    assert.deepEqual(JSON.parse(lastSendArgs.MessageBody), { tag: 'posts' });
});

test('handleRevalidate decodes base64 bodies before parsing', async () => {
    process.env.ND_REVALIDATION_QUEUE = 'https://sqs.us-east-1.amazonaws.com/123/q';
    sendBehavior = 'ok';
    const res = await handleRevalidate(jsonEvent(JSON.stringify({ path: '/blog' }), { base64: true }));
    assert.equal(res.statusCode, 202);
    assert.deepEqual(JSON.parse(lastSendArgs.MessageBody), { path: '/blog' });
});

test('handleRevalidate returns 502 when SQS send rejects', async () => {
    process.env.ND_REVALIDATION_QUEUE = 'https://sqs.us-east-1.amazonaws.com/123/q';
    sendBehavior = 'throw';
    const res = await handleRevalidate(jsonEvent(JSON.stringify({ tag: 'posts' })));
    assert.equal(res.statusCode, 502);
    assert.match(JSON.parse(res.body).error, /simulated SQS failure/);
});

test('handler is an async function exported by bridge.js', () => {
    assert.equal(typeof bridge.handler, 'function');
    assert.equal(bridge.handler.constructor.name, 'AsyncFunction');
});
