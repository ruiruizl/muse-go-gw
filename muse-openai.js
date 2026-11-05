/*
 * Muse OpenAI Proxy for Loon (v1)
 * 把 api.openai.com/v1/* 改写指向自建 muse2api 网关，并替换鉴权头。
 * 参数：base = 网关根地址（含 /v1），key = 网关密钥。
 * 用法：Loon → 配置 → 脚本 → 新增 → 粘贴本脚本，参数填 {"base":"..."}。
 */
function normalizeArg(v) {
  if (v === null || typeof v === "undefined") return {};
  if (typeof v === "object") {
    var o = {};
    for (var k in v) {
      if (Object.prototype.hasOwnProperty.call(v, k)) {
        var raw = v[k];
        if (raw === null || typeof raw === "undefined") continue;
        var s = String(raw).trim();
        if (s && s !== "none" && s !== "REPLACE_ME") o[k] = s;
      }
    }
    return o;
  }
  return {};
}
var A = normalizeArg(typeof $argument === "undefined" ? null : $argument);
var BASE = (A.base || "http://127.0.0.1:18610/v1").toString().replace(/\/+$/, "");
var KEY = A.key && A.key !== "none" ? A.key : "test-key";

function main() {
  if (typeof $request === "undefined" || !$request || !$request.url) { $done({}); return; }
  var m = $request.url.match(/^https?:\/\/api\.openai\.com(\/v1\/.*)$/);
  if (!m) { $done({}); return; }
  var newUrl = BASE + m[1];
  var h = {};
  var skip = { host: 1, authorization: 1, "content-length": 1, "accept-encoding": 1 };
  for (var k in ($request.headers || {})) {
    if (!skip[String(k).toLowerCase()]) h[k] = $request.headers[k];
  }
  h["Authorization"] = "Bearer " + KEY;
  $done({ url: newUrl, headers: h });
}
main();
