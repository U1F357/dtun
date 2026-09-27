#!/usr/bin/env python3
import json,pathlib
root=pathlib.Path(__file__).resolve().parents[1]/'reports'/'pacer'
result=[]
for mode in ['base','80']:
 for phase in ['forward','reverse','duplex-forward','duplex-reverse']:
  path=root/f'{mode}-{phase}.json'
  if not path.exists():continue
  j=json.loads(path.read_text());end=j.get('end',{})
  if not end:continue
  start=j['start']['timestamp']['timesecs'];duration=j['start']['test_start']['duration'];row=dict(mode=mode,phase=phase,mbps=end['sum_received']['bits_per_second']/1e6,tcp_retransmits=end['sum_sent'].get('retransmits'))
  for host in ['bj','hk']:
   p=root/f'{mode}-{host}-cpu.json'
   if not p.exists():continue
   m=json.loads(p.read_text());samples=[s for s in m['samples'] if start+1<s['time']<start+duration]
   if samples:row[host]=dict(mean_cpu_one_core_percent=sum(s['cpu_percent'] for s in samples)/len(samples),peak_cpu_one_core_percent=max(s['cpu_percent'] for s in samples),max_rss_mib=max(s['rss_mib'] for s in samples),samples=len(samples))
  result.append(row)
print(json.dumps(result,indent=2))
