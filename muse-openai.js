// Muse OpenAI 反代 · Loon 脚本 v1.1
// 把 api.openai.com/v1/* 转发到 Muse 网关，并强制替换 Bearer 钥匙。
const A = typeof $argument !== "undefined" && $argument
  ? (typeof $argument === "string" ? JSON.parse($argument) : $argument)
  : {};
const BASE = (A.base || "").toString().replace(/\/+$/, "");
const KEY = (A.key || "").toString();

(function () {
  if (typeof $request === "undefined" || !$request || !$request.url) {
    $done({});
    return;
  }
  const m = $request.url.match(/^https?:\/\/api\.openai\.com(\/v1\/.*)$/);
  if (!m) {
    $done({});
    return;
  }
  if (!BASE) {
    $done({});
    return;
  }
  const headers = {};
  const skip = { host: 1, authorization: 1, "content-length": 1, "accept-encoding": 1 };
  const src = $request.headers || {};
  for (const k in src) {
    if (skip[String(k).toLowerCase()]) continue;
    headers[k] = src[k];
  }
  if (KEY) headers["Authorization"] = "Bearer " + KEY;
  $done({ url: BASE + m[1], headers: headers });
})();
