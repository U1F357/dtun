# 首轮验证报告（2026-09-27，Asia/Shanghai）

> 历史快照。后续已加入 80 Mbps pacer，并按用户要求撤销经隧道的默认路由；当前状态和新增稳定性结果见 pacer-stability.md。

## 结论

**TUN + DTLS/UDP 方案已经在两台指定服务器部署并真实打通。** 北京 aliyun-BJ-200 → 香港 aliyun-HK 的 UDP 80，使用 DTLS 1.2 ECDHE-ECDSA AES-128-GCM、双向固定 SPKI 身份认证；TUN MTU 1500。香港作为 IPv4 NAT 出口。独立 `dtun-exit` 命名空间中的默认路由及 DNS 均经过香港。

这是功能/语义验证版，并不等于已具备生产级性能和运维能力。按照本次指示，没有尝试 HY2，也没有做 HY2 对照；不能推出“去除第二层拥塞控制一定有性能收益”。

## 公网实测

| 项目 | 结果 |
|---|---|
| 北京 → 香港 TCP，10 秒，单流，接收 goodput | 55.329 Mbps；sender 96 次 TCP retransmits |
| 香港 → 北京 TCP，10 秒，单流，接收 goodput | 51.940 Mbps；sender 779 次 TCP retransmits |
| IPv4 `ping -M do -s 1472` | 10/10 成功，1500 字节 IP 包；平均 RTT 61.119 ms |
| UDP，1472 字节应用载荷，5 Mbps，5 秒 | 2123 个数据报，0 丢包；jitter 0.038 ms |
| 命名空间 HTTPS 出口 IP | 47.242.247.74 |
| 香港 eth0 抓包 | 223287 个外层 UDP 数据报；最大 UDP 载荷 1153 B；0 外层 IP 分片 |
| 外层 DTLS application record 序号 | 223287 个记录，观察到重复序号 0 次 |
| 强制终止香港 daemon | 22:18:02 SIGKILL；22:18:06 systemd 恢复；22:19:17 北京自动重连成功 |

TCP retransmits 是内层 Linux TCP 的行为，隧道没有重传 DATA。DTLS 握手可以按标准重传。1500 字节 IP 包被拆为 1100 + 400 B，连同 framing 和 DTLS 开销形成 1153 + 453 B UDP 载荷；对应 IPv4 外层 IP 大小为 1181 + 481 B，低于 underlay 1500。

本轮的正向/反向测试都存在 TCP 重传，香港发送队列累计观察到 471 次整包队列丢弃。故不能把本轮吞吐当作链路容量或协议最优值。未做重复抽样置信区间、p99 RTT、daemon CPU profile 或与直连/HY2 的同条件对照。iperf JSON 的 CPU 字段代表 iperf，不代表 daemon。

首次 UDP iperf 尝试没有产生有效 interval，并报控制连接 Broken pipe；保留 `public/udp.json` 作为失败记录。改为实际出口命名空间重新运行后得到 `public/udp-netns.json` 中的有效结果，表格只使用后者。

公网原始数据在 `public/`；原始 pcap 保留于香港 `/opt/dtun-poc/reports/public/outer.pcap`，未把较大的 pcap 放入交付压缩包。

## namespace 实验

北京宿主上的三个隔离命名空间：client → router/netem → server。测试完成后均已自动删除，留下的 `dtun-exit` 是实际使用的出口命名空间。

- IPv4 ICMP 1500 B DF、双向 TCP、UDP 均通过。
- IPv6 `ping -6 -M do -s 1452` 通过（仅 TUN 内 IPv6，未声明 IPv6 Internet 出口）。
- 首次 baseline 抓包：401182 个外层 UDP；最大 1153 B；0 外层 IP 分片。
- 两端 TUN 的 **213295 个完整 TCP IP 包逐字节相同**，包含 sequence、ACK、flags、options 和 payload，证明 daemon 原样搬运 L3，未终止 TCP。
- 第二轮矩阵前 baseline 再次验证 154841 个相同 TCP 包。两个抓包的未匹配包可来自队列丢包或抓包丢包，未声称全程无损；原始抓包统计也保留在 lab 目录。

### netem 矩阵

100 Mbps underlay rate；每个方向加 RTT/2 延迟及指定随机外层丢包率。每格一个 10 秒 TCP 测试。表中为 receiver goodput（Mbps），括号为 TCP sender retransmits。0% 指未注入随机 loss，仍可能有队列/主机丢包。一次试验的随机波动可能打乱单调性。

| RTT | 0% | 0.1% | 1% | 3% |
|---|---:|---:|---:|---:|
| 50 ms | 69.77 (90) | 5.73 (7) | 1.73 (29) | 0.81 (40) |
| 100 ms | 53.02 (85) | 5.19 (8) | 1.25 (23) | 0.23 (24) |
| 200 ms | 21.84 (722) | 9.52 (260) | 0.87 (15) | 0.16 (13) |

两片中的任意一片丢失，完整 IP 包都无法交付；因此内层包丢失率可能高于单个外层数据报的 loss。无 FEC/ARQ 时，这是本方案的预期代价。该矩阵说明应进一步评估有损路径，而不是直接推荐生产替换。

## 软件测试

- `go test -race ./...` 和 `go vet ./...` 通过。
- codec：round trip，版本/type/flags/reserved、short header、长度/offset/溢出、零 payload 非法输入。
- fragmentation：1、100、1100、1101、1499、1500 字节完整重建。
- reassembly：随机乱序、重复、重叠、冲突长度、缺片、硬超时、迟到片不复活、count/bytes 硬限制、上万 PacketID 有界性。
- Packet ID：随机 session 初值、递增、wrap 拒绝。
- 真实 loopback DTLS：相互认证、保留报文边界、双向错误 pin 拒绝、1201 字节实际 UDP 写入拒绝。
- 密文代理注入：先篡改尚未收到的 record，再发送原件及 replay，接收端只交付原件一次；证明篡改和重放不会产生额外 application data。
- 两个 fuzz target，短时 fuzz 最后一次 codec 867041 次、reassembly 110984 次，无 panic；此前也运行过 5 秒 smoke fuzz。不是长期 fuzz/DoS 认证。

## 工程选择与范围差异

- Pion main 的 DTLS 1.3 仍有 tagging warning，因此 pin 稳定 v3.1.10（调查见 phase0）。Config API 虽已标记 deprecated，但本版本仍支持，且已封装在 transport 内。
- 没有暴露不加密的 raw UDP 运行模式；codec/分片单元测试后直接进入 loopback DTLS 和 namespace 实验。未单独交付草案 Phase 1 的 raw UDP CLI。
- 采用 CLI flags + systemd EnvironmentFile，没有引入 YAML 配置依赖；1200/1100/1500 等 PoC 参数固定。
- 外层 IPv4，IPv4 DF；内部支持普通 IPv4/IPv6 IP 包，但公网出口仅 IPv4。
- 可选 fixed pacer 未实现，默认不限速；没有任何 RTT/loss 反馈控制。
- root PoC，没有完成降权、证书自动轮换、多 peer、防大规模 handshake flood/FD leak 持续压测或长时间 soak test。
- reassembly 的内存计数保守计入 payload 和 span 元数据，4 MiB 限制可能先于 1024 包限制生效。4096 个 packet ID 窗口是应用去重边界；超出此窗口的极端乱序会丢包。
- 1 小时重建会话属于工程 key lifetime 限制，尚未等待满 1 小时验证；实际验证了进程异常退出后自动重连。
- systemd 的 setup 操作需要主机 mount namespace，故本版不启用会隔离 mount 的 ProtectSystem/PrivateTmp；保留 NoNewPrivileges 和 UMask。
- 北京既有 firewalld 与 Docker 同时过滤 FORWARD，必须同时增加本项目的精确许可。仅增加指定 namespace→TUN 的规则，没有全局停用防火墙。firewalld reload 后需重跑 setup-client 或重启本服务。
- 北京策略表附带 unreachable 默认路由，TUN 消失时避免策略查表落回普通公网出口。

## 当前部署状态

两端 dtun-poc.service 已启用开机启动并运行；香港 UDP 80。香港 NAT/路由、北京源地址策略路由和默认走隧道的 `dtun-exit` 已配置。现有北京 SSH、Docker 和主机默认路由保留。

临时 iperf 和 tcpdump 服务已停止；实验 namespace 自动清理。操作方法、证书生成、复现脚本及回滚见 README。
