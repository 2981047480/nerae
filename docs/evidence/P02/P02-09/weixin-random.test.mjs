import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const adapterPath = process.env.NERAE_WEIXIN_RANDOM_MODULE;
assert.ok(adapterPath, 'compiled adapter path must be provided by verify-weixin.sh');
const { generateWeixinRequestKeyWith } = require(adapterPath);

const expectedVector = 'AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8';
const makeBytes = () => Uint8Array.from({ length: 32 }, (_, index) => index).buffer;

function apiThat(callback) {
  return {
    getRandomValues(options) {
      assert.equal(options.length, 32);
      callback(options);
    },
  };
}

test('encodes exactly 32 callback bytes as canonical unpadded base64url', async () => {
  const key = await generateWeixinRequestKeyWith(apiThat(options => {
    options.success({ randomValues: makeBytes(), errMsg: 'getRandomValues:ok' });
  }));
  assert.equal(key, expectedVector);
  assert.equal(key.length, 43);
  assert.match(key, /^[A-Za-z0-9_-]{43}$/);
  assert.deepEqual(Buffer.from(key, 'base64url'), Buffer.from(makeBytes()));
  assert.ok(!key.includes('='));
});

test('rejects a missing API without fallback', async () => {
  await assert.rejects(generateWeixinRequestKeyWith(undefined), { message: 'secure_random_unavailable' });
});

test('rejects API failure and synchronous exceptions with a fixed error', async () => {
  await assert.rejects(generateWeixinRequestKeyWith(apiThat(options => options.fail({ errMsg: 'private' }))), {
    message: 'secure_random_unavailable',
  });
  await assert.rejects(generateWeixinRequestKeyWith({ getRandomValues() { throw new Error('private'); } }), {
    message: 'secure_random_unavailable',
  });
});

test('rejects an asynchronous callback error rather than leaving the request pending', async () => {
  const pending = generateWeixinRequestKeyWith(apiThat(options => {
    setImmediate(() => options.success({
      get randomValues() { throw new Error('private callback detail'); },
      errMsg: 'getRandomValues:ok',
    }));
  }));
  await assert.rejects(pending, { message: 'secure_random_unavailable' });
});

test('rejects non-ArrayBuffer and every length other than 32', async () => {
  for (const randomValues of [new Uint8Array(32), new ArrayBuffer(31), new ArrayBuffer(33)]) {
    await assert.rejects(generateWeixinRequestKeyWith(apiThat(options => {
      options.success({ randomValues, errMsg: 'getRandomValues:ok' });
    })), { message: 'secure_random_unavailable' });
  }
});

test('settles once if a buggy API calls success more than once', async () => {
  const key = await generateWeixinRequestKeyWith(apiThat(options => {
    options.success({ randomValues: makeBytes(), errMsg: 'getRandomValues:ok' });
    options.fail({ errMsg: 'late failure' });
  }));
  assert.equal(key, expectedVector);
});
