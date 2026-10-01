# dtun

一个用 Go 编写的 Linux / Windows 点到点加密 L3 隧道：TUN + DTLS/UDP，可承载 IPv4/IPv6 的 TCP、UDP 和 ICMP 流量。

**这是一个由 AI（OpenAI Codex）根据用户需求创建并持续迭代的项目，包括代码、文档和测试。当前为实验性实现，未经独立安全审计。**

[![Build and release](https://github.com/U1F357/dtun/actions/workflows/build.yml/badge.svg)](https://github.com/U1F357/dtun/actions/workflows/build.yml)

## 功能

- DTLS 1.2 加密，双方通过 SHA-256 SPKI pin 验证对端，检查证书有效期。
- 服务端多 UDP 端口监听，客户端可定时切换端口。
- 预连接与软切换：旧 IP 包在旧通道完成发送，新包使用新通道；旧通道继续接收一段可配置的宽限时间。
- 应用数据帧在加密前增加 1–20 字节随机 padding。
- TUN MTU 可配置为 576–9000，默认 1500；IPv6 要求至少 1280，两端应保持一致。
- 内层 IP 包在隧道内分片、重组，不要求物理网卡支持相同大小的 MTU。
- 固定速率 pacer，以及可配置的队列、重组容量、超时和 socket 缓冲区。

## 下载与构建

从 [Releases](https://github.com/U1F357/dtun/releases) 下载 Linux / Windows 的 amd64 或 arm64 压缩包及 `SHA256SUMS`。在下载目录校验并解压，例如：

```sh
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf dtun_linux_amd64.tar.gz
./dtun --help
```

Linux 运行需要`/dev/net/tun`、iproute2，以及 root 或适当的 `CAP_NET_ADMIN` 权限。Windows 从 v0.2.0 起提供 Wintun 客户端，详见 [Windows 使用说明](docs/WINDOWS.md)。目前不支持 macOS。

源码构建需要 Go 1.24 或更高版本；CI 使用当前稳定版 Go：

```sh
make build   # Linux amd64，输出 ./dtun
make check   # race 测试和 vet
# 交叉编译 ARM64
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dtun-arm64 ./cmd/dtun
```

## 最小连通性示例

以下地址为文档示例，需替换为真实公网地址：服务端 `198.51.100.10`，客户端 `192.0.2.10`。端口可自行选择；以下使用 `20000,20001,20002` 作为示例。请按实际配置放行对应 UDP 端口。

在各自主机上生成身份，私钥留在本机：

```sh
# 服务端执行
sh scripts/certgen.sh ./identity server
# 客户端执行
sh scripts/certgen.sh ./identity client
```

通过可信途径交换 `.pin` 文件；pin 是公钥指纹，不能替换成证书文件的哈希。将对端 pin 保存为 `identity/client.pin` 或 `identity/server.pin`。

服务端：

```sh
sudo ./dtun --server --endpoint 0.0.0.0:20000 --allow-ip 192.0.2.10 \
  --ports 20000,20001,20002 --address 10.255.255.2/30 --mtu 1500 \
  --cert identity/server.crt --key identity/server.key \
  --peer-pin "$(cat identity/client.pin)" --max-rate-bps 80000000
```

客户端：

```sh
sudo ./dtun --endpoint 198.51.100.10:20000 --ports 20000,20001,20002 \
  --switch-interval 10m --address 10.255.255.1/30 --mtu 1500 \
  --cert identity/client.crt --key identity/client.key \
  --peer-pin "$(cat identity/server.pin)" --max-rate-bps 80000000
```

客户端另开终端测试：`ping 10.255.255.2`。这些命令仅创建 TUN 及直连路由，**不设置默认路由**。若需要通过服务端访问其他网络，还需按实际拓扑配置明确的目标路由、转发和 NAT；仓库中的部署脚本属于实验环境示例，应先阅读再使用。

## MTU 与限制

TUN MTU 和外层网络 MTU 是两个不同的限制。大内层包会拆成多个加密 UDP 数据报传输；外层没有动态 PMTU 探测，底层路径仍需容纳当前数据报。服务端解封装后若需要转发到更小 MTU 的出口，依赖正常的 IP 转发行为：IPv4 DF 包返回 ICMP “需要分片”，非 DF 包可分片，IPv6 返回 Packet Too Big。应允许相关 ICMP 返回，避免 PMTU 黑洞。

软切换能减少切换中断，但不保证零丢包。公网丢包、队列溢出、重组超时或超过旧通道宽限期都可能导致丢包。应用的 TCP 连接通常可跨切换维持，仍取决于网络、应用超时及路由/NAT 状态。

固定速率 pacer 不具备拥塞反馈；80 Mbps 是发送上限，不是吞吐保证。当前没有 ARQ、FEC、公平队列或自适应拥塞控制。轮换端口及随机 padding 也不能保证规避运营商限速。暂未实现运行时降权与证书自动轮换。

详细参数见 [CONFIGURATION.md](CONFIGURATION.md)。历史实验记录见 [reports](reports/) 和 [实验部署笔记](docs/LAB-NOTES.md)；历史数据对应各自的版本与环境，不代表所有网络下的性能。

## 自动编译与 Release

GitHub Actions 在推送 main、提交 PR 或手动触发时执行 Linux / Windows 原生 race 测试、vet、真实 Linux TUN 关闭测试及 Windows Wintun 收发/清理测试，生成两种系统的 amd64 / arm64 压缩包。ARM64 目前只做交叉编译，未做原生运行测试。

推送 `v*` 标签会在检查通过后创建 GitHub Release，附带 Linux 和 Windows 两个架构的压缩包和 SHA-256 校验文件：

```sh
git tag v0.2.0
git push origin v0.2.0
```

`v0.*` 和包含 `-` 的版本标记为预发布。已发布版本应使用新标签更新，避免覆盖已有发布产物。
