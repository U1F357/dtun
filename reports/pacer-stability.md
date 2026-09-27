> 后续更新：本报告记录的是重组修复前的测试。过载时异常放大的丢包已针对性改善，见 [重组修复与对照实验](reassembly-improvement.md)；原始结果保留供比较。

# 80 Mbps pacer、CPU 与扩展稳定性验证

测试日期：2026-09-27。Go 实现，Pion DTLS 1.2，香港 测试 UDP 端口；两端均为 2 vCPU Intel Xeon Platinum 虚拟机，具有 AES 指令。

## 当前路由与部署

**遵照最新要求，不使用隧道作为默认路由。** 北京主机原默认路由仍是经 eth0/172.25.63.253；11880/11881 两条源地址策略已删除，表 51880 清空；`dtun-exit` 只路由到 `10.255.255.0/30`，没有默认路由。隔离实验的 underlay 也改为明确的网段路由。systemd 启动脚本不会添加隧道默认路由。

两端实际运行参数已加入 `--max-rate-bps 80000000`。香港 NAT 规则保留，但当前不通过隧道承载默认 Internet 流量。所有本轮性能测试目标都是明确的 `10.255.255.2`。

## Pacer 实现

固定 token bucket 位于 UDP WriteTo 前，按实际密文长度 + IPv4 20 B + UDP 8 B 计费。每个方向分别 80,000,000 bit/s，burst 16,384 B（80 Mbps 下约 1.64 ms）。`--max-rate-bps 0` 关闭。没有 RTT/loss 反馈、ACK、ARQ 或可靠队列；原来的 256 个完整内层包队列保持有界，满队列丢整包。

速率包含封装开销，所以不能把 80 Mbps 理解为 TCP payload 80 Mbps。典型 1500 B 内层包形成 1181 + 481 B 外层 IPv4 包，1448 B TCP payload 对应的理想上限约 69.7 Mbps，还没扣 ACK、重传等消耗。

香港 eth0 抓包统计 1,000,276 个 UDP 包，最大 UDP 载荷 1153 B，外层 IP 分片 0。固定 1 秒分桶最高：北京→香港 80.0419 Mbps，香港→北京 80.0153 Mbps。允许的 16 KiB burst 可使短窗口略高于 80；结果在 token bucket 的 `80 Mbps × interval + burst` 边界内。记录在 `pacer/80-outer-rate.json`；完整 snaplen=128 pcap 保留于香港服务器。

## 公网性能与 CPU

以下 TCP 均为 receiver goodput。单向每次 15 秒，双向同时传输每方向 2 条 TCP、20 秒。CPU 用 `/proc/<daemon PID>/stat` 每秒取差，覆盖进程全部线程；**100% 表示一个逻辑核心**，两台机器各有 2 核，总容量为 200%。不是 iperf 进程的 CPU 数字。RSS 是隧道进程实际驻留内存。

| 模式 / 方向 | TCP Mbps | TCP 重传 | 北京平均 CPU | 香港平均 CPU |
|---|---:|---:|---:|---:|
| 不限速 / 北京→香港 | 49.87 | 21 | 19.6% | 39.4% |
| 不限速 / 香港→北京 | 76.11 | 1972 | 34.3% | 49.7% |
| 80 Mbps / 北京→香港 | 67.89 | 121 | 31.6% | 60.7% |
| 80 Mbps / 香港→北京 | 63.09 | 995 | 39.1% | 52.4% |
| 80 Mbps / 同时双向：北京→香港 | 59.99 | 309 | 50.5% | 90.5% |
| 80 Mbps / 同时双向：香港→北京 | 53.66 | 89 | 50.5% | 90.5% |

双向同时测试的两行共享同一个时间窗口，CPU 不能把两行相加。80 Mbps 单向期间 RSS 约 11.9–13.1 MiB；双向时香港平均约占一个核心 90.5%，单秒峰值约 108.0%，换算整台 2 vCPU 的平均容量约 45.3%、峰值 54.0%。北京双向平均一个核心的 50.5%。因此 CPU 有明显开销，但本次未打满整台机器；不能外推到几百 Mbps 或更多 peer。

不限速与 80 Mbps 各为一轮测试，公网状态会变化；不能将结果解释为 pacer 必然提高速度或降低 CPU。不限速反向这轮达到 76.11 Mbps，但 1972 次重传；限速反向 63.09 Mbps、995 次重传；正向吞吐从 49.87 升到 67.89 Mbps，但重传从 21 升到 121。证据支持“限速确实工作”，不支持无条件优越性。

额外以 100 Mbps UDP offered load 压入 80 Mbps 外层上限，报告丢包 29.02%，属于主动超额输入的预期丢弃；服务继续运行，随后双向 TCP 和 1500 B ping 成功。不要把该 UDP JSON 中发送端约 100 Mbps 的 rate 误认为隧道突破 80 Mbps。

## 扩展稳定性场景

1. **120 秒混合压力**：同时两个正向 TCP、两个反向 TCP、5 Mbps UDP（1472 B）及每 200 ms 一次 1500 B DF ICMP。正/反向 TCP 分别 58.05/62.87 Mbps，UDP 丢包 0.139%，ICMP 599/600 成功（丢包 0.167%）；满载 ICMP 平均 RTT 32.18 ms、最大 102.61 ms。应用正常结束，无 daemon 异常退出；有界队列并不等于满载零延迟/零丢包。结果 `stability/soak-*.json`。
2. **包长边界**：ICMP payload 0/1/64/1072/1073/1471/1472 B（IP 总长 28–1500 B）均通过；1473 B 加 DF 被本机 MTU 拒绝，符合预期。
3. **复合故障**：每方向 50±15 ms 延迟、0.5% 丢包、2% 重复、15% reorder，持续 TCP 25 秒，TCP goodput 1.449 Mbps，连接保持并完成。
4. **带宽骤降/过载**：underlay 每方向降至 20 Mbps、queue limit=100，同时发送 100 Mbps UDP，UDP 丢包达到 **96.99%**，并触发重组超时和资源限制；没有无界积压或崩溃，恢复后正常传输。这是明确的性能弱项：固定 pacer 不会跟随 underlay 自动降速，两片重组和 incomplete 表饱和会进一步放大丢包。不能因进程存活就把该工况评为吞吐良好；带宽下降时应手动降低 rate，并后续研究重组元数据占用/准入策略。
5. **短断网**：100% 丢包约 10 秒；断网期间 ping 失败，恢复后 ping 成功，不要求应用数据重传。
6. **长断网**：100% 丢包 90 秒，超过 75 秒 session timeout。22:43:50 两端结束旧 session，恢复链路约 22:44:06，22:44:09 两端建立新 session，约 3–4 秒后恢复；不需要人工重启。
7. **未认证异常 UDP**：从非当前 session 的源端口发送 10000 个随机 64/1300 B UDP 包，不能注入 TUN，随后 1500 B ping 成功，daemon 保持运行。它验证固定 peer/源端口过滤及有限压力，不等同全面 DDoS 验证。
8. **空闲恢复**：故障后空闲 35 秒，保活继续，重组占用回到 0，goroutine 回到 12；FD 外部采样一直为 7，runtime 自采样为 8（读取 `/proc/self/fd` 时包含临时目录 FD）。
9. **服务/路由重启与重复配置**：实际重启两端服务、重复执行 setup-client 两次，恢复连接。测试发现并修复旧脚本用 `metric 0` 清理默认路由时误删现行路由的问题。随后按用户最新要求彻底撤掉本项目默认路由逻辑；最终再次检查无隧道默认路由。
10. **上一轮真实 SIGKILL**：香港进程崩溃后由 systemd 恢复，北京约 75 秒后重新建连；日志仍在 `public/crash-timeline.txt`。本轮新增 80 Mbps 版本另通过上述 90 秒完全断网的会话重建。

混合压力阶段两端 RSS 大致 9.5–12 MiB，FD 始终 7；复合故障/瓶颈阶段服务器 RSS 曾增加，空闲及重连后回落至约 11 MiB。没有观察到 FD、goroutine 或 RSS 持续单调增长。Go GC 会保留已向 OS 申请的 heap，RSS 不要求立即回到启动值。

第一次完整 lab 的后半段因 tc 不接受 `.5%` 语法而中止，不是 daemon 故障。修正为 `0.5%` 后保留已完成的 120 秒压力数据，在新 lab 重跑全部故障部分，最终日志 `stability-faults/scenarios.log` 为 ALL STABILITY SCENARIOS PASSED。原始成功/失败记录均保留。

## 单元 / 模糊测试

`go test -race ./...` 与 `go vet ./...` 通过。新增：固定 rate/burst/cancellation/低速边界、真实时间持续发送上限、关闭 pacer；会话层缺片超时后不重传、不交付残包，迟到片不复活，malformed frame 不阻断后续正确流量，重复片只交付一次；100 次会话创建/销毁后 goroutine 未持续增长。原有双向 pin 拒绝、密文篡改和重放测试继续通过。

decoder 和 reassembly 各补跑 10 秒 fuzz，结果在 `fuzz-codec.txt` / `fuzz-reassembly.txt`。这些是有限时长的验证，不构成长期安全证明。

## 可复现命令与边界

- `scripts/stability-lab.sh`：在 Linux root 环境完整重跑（约 6 分钟），只对隔离 router namespace 注入 netem，退出清理三个 lab namespace。
- `scripts/pacer-benchmark.sh`：北京执行；香港先在 TUN 地址启动 5209/5210 两个 iperf3 server，不需要公网开放这些端口。
- `scripts/sample-cpu.py`：采样指定 daemon PID；`scripts/summarize-pacer.py` 汇总。
- `scripts/pcap-rate.py`：基于 IPv4/UDP 头统计外层速率与大小，支持截短抓包。

按要求没有做降权和证书轮换，也没有尝试 HY2。当前完成分钟级混合压力和多种故障测试；没有数天 soak、全机重启、线路动态换 IP、无限规模攻击或完整硬件性能画像。仍是经更多验证的 PoC。
