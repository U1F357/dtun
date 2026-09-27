# TUN + DTLS/UDP 点到点 L3 隧道 PoC

本地整理版（2026-09-28）：增加可调 TUN MTU/资源参数、满队列零分配路径及可取消 TUN I/O。已在隔离网络验证，本次未升级两台服务器的在用服务；它们仍运行此前的软切换版。

本项目搬运完整 IP 包，不终止业务 TCP。已部署于 aliyun-BJ-200 ↔ aliyun-HK，香港监听 **多个自定义 UDP 端口** 并作为 IPv4 NAT 出口。TUN MTU 默认 1500，可通过 `--mtu` 设为 576–9000；外层 UDP 载荷上限 1200，启用随机 padding 后实际 AES-GCM 数据包最大 1173 字节。

## 当前测试模式：不使用隧道默认路由

按用户要求，主机原有默认路由保持不变，策略表 51880 中不设置默认路由，`dtun-exit` 命名空间也没有默认路由。命名空间只增加 `10.255.255.0/30` 的明确目标路由。

在北京服务器上：

```bash
ip netns exec dtun-exit ping -M do -s 1472 -c 5 10.255.255.2
# 香港开启 iperf3 -s -B 10.255.255.2 -p 5209 后：
ip netns exec dtun-exit iperf3 -c 10.255.255.2 -p 5209 -t 15
```

此前已验证香港 NAT 公网出口，但当前测试配置不会默认将 Internet 流量送入隧道。香港 NAT 规则仍保留，后续如需使用出口，应明确配置目标路由。

两端管理：

```bash
systemctl status dtun-poc
journalctl -u dtun-poc -f
systemctl restart dtun-poc
```

源码/二进制：`/opt/dtun-poc/`；私钥、证书和参数：`/etc/dtun/`；服务：`/etc/systemd/system/dtun-poc.service`。私钥在各自服务器生成，权限 0600，未下载到交付目录。服务已设置开机启动。

## 协议与依赖

- Go 1.27.1 构建，Pion DTLS v3.1.10，`golang.org/x/sys v0.41.0`。
- DTLS 1.2 + ECDHE/ECDSA + AES-128-GCM；双向 SHA256 SPKI 固定指纹及证书有效期检查；Pion 负责握手、密码学和 1024-record 防重放窗口。
- v2 framing：16 字节头，每片最多 1100 字节；1500 字节包拆为 1100 + 400。每个 DATA/PING/PONG 帧追加 1–20 字节密码学随机 padding，长度均匀随机；padding 长度在头部，头与 padding 一起经 DTLS 加密认证。握手报文不额外填充。v2 与原 v1 不兼容，需要两端一起升级。
- DATA 无 ACK、无重传、无可靠队列、无反馈拥塞控制。已实现可选固定 pacer：`--max-rate-bps 80000000`，当前两端启用，`0` 关闭。按实际外层 IPv4 总字节（含 IP/UDP/DTLS 开销）计费，16 KiB token bucket burst，无 RTT/loss 反馈。
- 全包发送队列 256；重组最多 1024 包、4 MiB 活跃条目预算、1 秒硬超时；按实际分片索引容量计费，另计每条目 192 字节开销额度（不是进程 RSS 上限）。表满淘汰最早未完成包，完整单片包直接通过；4096 个 ID 的有界去重窗口阻止迟到片重新占位。
- 20 秒空闲 PING，75 秒无有效业务/控制帧重连；每小时重建会话更新密钥，不是 DTLS 1.3 KeyUpdate。会话切换会短暂丢包。
- 单一固定 peer；服务器除证书校验外还检查公网源 IP。北京公网 IP 改变需更新 `--allow-ip`。
- 日志每 15 秒输出 JSON counters，不记录私钥或流量密钥。

版本/API 调查见 `reports/phase0.md`。首轮历史测试见 `reports/validation.md`，新增 80 Mbps / CPU / 稳定性测试见 `reports/pacer-stability.md`；重组过载修复与对照实验见 `reports/reassembly-improvement.md`。

## 多端口与定时切换

在 `/etc/dtun/peer.env` 的 `DTUN_ARGS` 中配置：

```text
# 服务端：同一 endpoint 主机地址，同时监听列表内端口
# 以下端口仅为示例，请按实际配置替换。
--server --endpoint 0.0.0.0:20000 --ports 20000,20001,20002
# 客户端：从列表第一个端口开始，成功建连后每 10 分钟轮换
--endpoint 198.51.100.10:20000 --ports 20000,20001,20002 --switch-interval 10m
```

`--ports` 覆盖 endpoint 中的端口，最多 16 个，必须互不重复、范围 1–65535；省略时只使用 endpoint 原端口。`--switch-interval 0` 关闭定时切换，多端口列表仍在断线/建连失败后尝试下一端口。固定选择某个端口 可使用 `--ports 20000 --switch-interval 0`。非零切换间隔至少 1 秒，仅客户端且至少两端口可用；正式部署使用 10 分钟，短间隔仅用于压力测试。

切换计时到期后，后台新建 UDP socket（操作系统选择源端口）并重新进行 DTLS 认证，旧通道继续传输；候选端口失败时保留健康旧通道，再尝试其他端口。新会话认证完成后交接：旧发送器发完当前整个 IP 包，保留未发送的完整包队列；新发送器接手，旧接收器继续保留 1 秒处理迟到分片。新旧接收器分别解密/重组，完整 IP 包汇入同一个 TUN。不是将不同会话的密文混合解密，也不在两个通道重复发送业务包。

正常交接无需关闭 TUN 或修改路由，仍不承诺零丢包：真实网络丢包、超过 1 秒的迟到片、队列溢出或握手结果不对称均可能影响业务。所有端口使用相同证书和 peer pin；重组状态按会话隔离，每会话 4 MiB，短暂双接收期间合计预算可更高。并非多路径负载均衡。无法握手的候选端口最多等待约 10 秒，失败后等 2 秒尝试下一端口，这期间健康旧通道继续工作；已经失效的旧路径仍可能中断。

端口变化与随机包长是否缓解 ISP 限速尚未验证；它们不会把 DTLS 变成普通 HTTPS，也不能绕过按 IP/总流量或协议分类的所有限速。多端口/padding 初版测试见 `reports/ports-padding.md`，改进交接测试见 `reports/handover.md`。主机和测试命名空间仍不设置隧道默认路由。

需要定位本地 ICMP echo 丢弃时可临时开启 `--trace-icmp-drops`，记录队列满/未连接导致丢弃的地址、类型、identifier 与 sequence，不记录 payload；默认关闭。配合两端 TUN 抓包使用，汇总计数不能单独证明某个 ping 的丢失原因。

## 可调资源限制

TUN MTU、发送队列、重组容量/超时、旧通道排空时间、UDP 缓冲区和握手超时均支持配置，保留原默认值并校验范围。见 [配置说明](../CONFIGURATION.md) 与 [本轮检查记录](../reports/review-20260928.md)。队列仍为 FIFO；增大容量不代表实现公平排队。

## 构建与测试

```bash
go test -race ./...
go vet ./...
go test ./internal/proto -fuzz=FuzzDecode -fuzztime=10s
go test ./internal/reassembly -fuzz=FuzzReassembly -fuzztime=10s
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dtun ./cmd/dtun
```

Linux root 环境（需要 iproute2、tc、tcpdump、iperf3、openssl、python3）：

```bash
sudo scripts/netns-lab.sh
sudo MATRIX=1 OUT=reports/lab-matrix scripts/netns-lab.sh
```

实验只创建 `dtun-c`、`dtun-r`、`dtun-s` 三个命名空间，不修改宿主机 uplink 的 qdisc。若命名空间已存在则拒绝覆盖。退出自动清理。

矩阵为 100 Mbps underlay、RTT 50/100/200 ms、每方向外层丢包率 0/0.1/1/3%，每格 10 秒 TCP。属于短时验证，不是充分的性能排名。pcap 校验工具验证外层大小/分片，并比较两个 TUN 上完全一致的 TCP IP 包；允许有界队列导致丢包。

公网重测：香港先运行 `iperf3 -s -B 10.255.255.2 -p 5209`，北京运行 `scripts/benchmark.sh`。无需在公网开放 iperf 端口。

## 新机器部署

这是面向本次两台服务器的 PoC，脚本中的地址、路由表和接口须按新环境调整。先检查 `10.255.255.0/30`、`10.254.253.0/30`、表 51880、规则优先级 11880/11881 未被占用。

1. 构建并复制目录到 `/opt/dtun-poc`，安装上述 Linux 工具。
2. 各自生成身份：`scripts/certgen.sh /etc/dtun client` 或 `server`。脚本拒绝覆盖已有私钥。
3. 交换 `.pin` 文件中的公钥指纹，按 `configs/*.example.env` 写 `/etc/dtun/peer.env`；无需传输私钥。
4. 修改 `scripts/post-start.sh` 中的香港公网 IP、北京物理网关、接口，调整 setup 脚本地址。
5. 香港安全组放行来源北京公网 IP 的 **多个自定义 UDP 端口**（或自定义列表）；配置/检查本机防火墙。
6. 复制 `configs/dtun-poc.service` 到 `/etc/systemd/system/`，执行 `systemctl daemon-reload && systemctl enable --now dtun-poc`。

setup-server 只管理 `inet dtun_poc` 表，启用 IPv4 forwarding 并记录原值；setup-client 只增加隧道端点 host route、veth、命名空间和精确转发规则，并移除本项目旧的默认路由策略。香港脚本应在检查现有 nftables 规则后使用；独立表的 accept 不会覆盖其他表的 drop。

北京使用 firewalld nft 后端，除 iptables 精确 FORWARD 规则，还需在 firewalld 的 FORWARD chain 添加带 `dtun-poc-exit` 标记的精确命名空间→TUN 放行。脚本不会 reload 全机 firewalld。**如管理员 reload firewalld，应再运行 setup-client.sh 或重启 dtun-poc 服务恢复这条运行时规则。**

## 停用 / 回滚

```bash
# 北京
/opt/dtun-poc/scripts/rollback.sh client
# 香港
/opt/dtun-poc/scripts/rollback.sh server
```

只删除本项目创建的规则和网络对象、停用服务，保留源文件与证书。先退出 `dtun-exit` 内的应用再回滚。北京原先已启用 IP forwarding，保持原值；香港恢复部署前记录的值。

## 限制

这是验证版：长期进程仍为 root，未做完整降权/凭据轮换/长期运行压测；DTLS 1.3、HY2、FEC、ARQ、multiqueue、GSO/GRO 不在本次范围。已完成短时混合压力及故障恢复测试，详情见 `reports/pacer-stability.md`；尚未做数日 soak。内层 IP 数据是端到端的，但 1500 字节包对应两份 UDP，外层任一片丢失都会丢整个内层包。

没有 HY2 对照，不能据此宣称去除第二层拥塞控制带来性能优势。DTLS 仍可被识别和封锁，本项目不提供流量伪装。
