# muse-go-gw — 暮色 OpenAI 网关（Go 版，给小内存 VPS 用）

muse-protocol-go 协议库 + 一层 OpenAI 兼容 HTTP。编译后单个二进制，常驻内存约 20~30M，
128M VPS 可以和节点共存。

## 文件

- `main.go` — 网关本体
- `go.mod` — 用 replace 指向 `./sdk`（CI 里自动拉协议库放这儿）
- `.github/workflows/build.yml` — GitHub Actions 自动编译 linux amd64 + arm64

## 出二进制（两条路任选）

**A. 推到 GitHub 自动出**（推荐，本机编译不了 Go）
```sh
cd muse-go-gw
git init && git add -A && git commit -m init
gh repo create muse-go-gw --public --source . --push   # 或手动建仓库后 push
# Actions 跑完 → Artifacts 下载 muse-go-gw-linux-amd64 / arm64
```

**B. 任何有 Go 1.24 的机器上**
```sh
git clone https://github.com/CoolkHz/muse-protocol-go sdk
go build -o muse-go-gw .
```

## VPS 部署（Alpine）

```sh
# 传上去后
apk add tzdata  # 可选
mkdir -p /opt/muse
mv muse-go-gw /opt/muse/ && chmod +x /opt/muse/muse-go-gw

# 环境变量写进 /opt/muse/env：
#   MUSE_COOKIE_FILE=/opt/muse/cookies.txt
#   GATEWAY_KEY=换个自己的key
#   PORT=8130
# cookie 首次放一份进去：echo 'hatch_sess=...' > /opt/muse/cookies.txt

# OpenRC 服务 /etc/init.d/muse-go-gw ：
#!/sbin/openrc-run
command="/opt/muse/muse-go-gw"
command_background="yes"
pidfile="/run/muse-go-gw.pid"
directory="/opt/muse"
output_log="/var/log/muse-go-gw.log"
error_log="/var/log/muse-go-gw.log"
export MUSE_COOKIE_FILE=/opt/muse/cookies.txt
export GATEWAY_KEY=你的key
export PORT=8130

# rc-update add muse-go-gw && rc-service muse-go-gw start
```

## 验证

```sh
curl -s http://127.0.0.1:8130/healthz
curl -s http://127.0.0.1:8130/v1/models
curl -s http://127.0.0.1:8130/v1/chat/completions \
  -H "Authorization: Bearer 你的key" -H "Content-Type: application/json" \
  -d '{"model":"muse-spark","messages":[{"role":"user","content":"你好"}]}'
curl -s http://127.0.0.1:8130/admin/diagnose   # 会话/网关诊断
```

## 接客户端

- baseURL：`http://VPS_IP:8130/v1`（建议套一层 Caddy/Nginx 上 TLS，或用 Loon 插件指过去）
- apiKey：`GATEWAY_KEY` 的值
- 模型：`muse-spark` / `muse-image` / `muse-video`

## 说明

- cookie 轮换由协议库自动做，轮换结果写回 `MUSE_COOKIE_FILE`，重启不丢。
- 同一账号请求串行（sem=1），免费账号本来也不支持并发。
- 图片/视频落在 `data/media/`，通过 `/media/文件名` 直接取。
- 协议库本身：https://github.com/CoolkHz/muse-protocol-go （Noise XX + protobuf over WSS）
