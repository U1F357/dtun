# 可调资源参数

这些是本地运维参数，不改变 v2 wire format。两端可分别配置，但排空/重组时间建议一致。修改 `/etc/dtun/peer.env` 的 `DTUN_ARGS` 后重启生效；进程重启本身可能中断业务，不等同于定时软切换。

| 参数 | 默认 | 允许范围 | 用途 |
|---|---:|---:|---|
| `--mtu` | 1500 | 576–9000 | TUN 内层 IP 包 MTU；IPv6 至少 1280 |
| `--queue-packets` | 256 | 1–65536 | 全包发送 FIFO 容量 |
| `--reassembly-packets` | 1024 | 1–4096 | 每个接收会话的未完成包上限 |
| `--reassembly-bytes` | 4194304 | 1700–67108864 | 每会话活跃重组条目的计费预算 |
| `--reassembly-timeout` | 1s | 100ms–30s | 从首片到达起算的重组硬超时 |
| `--drain-timeout` | 1s | 100ms–30s | 交接后旧接收通道的保留时间 |
| `--socket-buffer-bytes` | 4194304 | 65536–67108864 | 每个 UDP socket 的收、发缓冲请求值 |
| `--handshake-timeout` | 10s | 500ms–1m | 候选 DTLS 握手的最长等待 |

显式传入 0、负数或越界值会在创建 TUN 前报错；客户端 `drain-timeout` 不得大于非零 `switch-interval`。内核可能将 UDP 缓冲请求值截断到系统上限；参数不是实际缓冲大小的保证。

示例（追加到已有 DTUN_ARGS，保留证书、endpoint 等必要项）：

```text
--queue-packets 512 --reassembly-packets 1024 --reassembly-bytes 4194304 --reassembly-timeout 1s --drain-timeout 1s --socket-buffer-bytes 4194304 --handshake-timeout 10s
```

默认仍为 256 包，不自动改大。队列容量改变只能调整突发容忍度、内存与等待时间的折中，无法解决长期输入超过输出带宽，也不能提供流间公平性。以全部 1500 字节、80 Mbps 为例，256 包仅内层字节的发送时间就约 38.4ms，1024 包约 153.6ms；封装与 padding 后实际时间更长。65536 包的载荷内存可接近 94 MiB，另有 slice/队列/GC 开销。

MTU 增大时，重组字节预算还必须足以容纳一个该 MTU 的完整包及索引；配置校验会计算所需最小值。队列载荷内存随 MTU 成比例变化，例如 9000 MTU × 65536 包约 562.5 MiB，不建议盲目使用范围上限。

重组字节预算含载荷、实际索引容量和条目开销额度，不等于进程 RSS 硬上限。新旧接收会话重叠时按会话分别限额，总预算会叠加。延长旧通道保留时间不能恢复网络真正丢掉的分片；如果重组超时更短，相关残片仍会先过期。4096 的包上限来自固定去重窗口，不能随意超出。

`--max-rate-bps`、`--ports`、`--switch-interval` 继续可配。UDP 载荷上限 1200、分片 1100、padding 1–20、去重窗口及密码套件保留为协议约束；没有把所有常量都变成任意旋钮。

## TUN MTU 与出口网卡 MTU

两端使用相同 `--mtu`，大于 1500 时两端都需升级到本版本；目前没有自动 MTU 协商。收到超过本地设置的包会拒绝并计入 `inner_mtu_exceeded_rx`，配置不一致可能造成丢包。IPv6 接口要求 MTU 至少 1280。

TUN 9000 并不要求外层网卡也为 9000：一个内层包被拆成多个至多 1100 字节载荷的加密分片。当前最大实际外层 IPv4 数据报为 1201 字节（1173 UDP payload + 28 IP/UDP header），UDP payload 防护上限仍为 1200。没有新增外层 PMTU 自适应；小于该封装需求的外层链路需另外调整分片预算，不能仅修改 TUN MTU。

香港重组后向 MTU1500 的出口转发时，Linux 对 IPv4 DF 包返回 ICMP fragmentation-needed；IPv4 非 DF 可分片；IPv6 路由器不分片，而返回 Packet Too Big。ICMP 必须能穿过防火墙并返回源端，否则会出现 PMTU 黑洞。TCP 依靠 MSS/PMTUD，UDP 应用需遵守 MTU或处理错误；这不是隧道保证任意大 UDP 都能无损转发。已用隔离出口验证这些行为，未将测试流量设为默认路由。

隔离实验可通过 `DTUN_RESOURCE_ARGS` 传上述数值参数给 `scripts/netns-lab.sh`；该变量按空格拆成参数数组，不作为 shell 代码执行。不要对运行中的业务实例使用实验命名空间脚本。
