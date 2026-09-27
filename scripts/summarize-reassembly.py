#!/usr/bin/env python3
import json,pathlib,sys,statistics
root=pathlib.Path(sys.argv[1]);rows=[]
for p in sorted(root.glob('*-*')):
 if not p.is_dir() or not (p/'server-iperf.json').exists():continue
 try:j=json.loads((p/'server-iperf.json').read_text())
 except json.JSONDecodeError:continue
 # iperf 3.9 server UDP end.sum bytes/rate are zero; use actual received intervals.
 ints=j['intervals'];total=sum(x['sum']['bytes'] for x in ints);duration=ints[-1]['sum']['end']-ints[0]['sum']['start']
 metrics={}
 for line in (p/'server.log').read_text().splitlines():
  if 'final stats ' in line:metrics=json.loads(line.split('final stats ',1)[1])
 cpu=json.loads((p/'server-cpu.json').read_text())['samples']
 rows.append(dict(run=p.name,received_mbps=total*8/duration/1e6,loss_percent=j['end']['sum']['lost_percent'],cpu_mean=sum(x['cpu_percent'] for x in cpu)/len(cpu),rss_peak_mib=max(x['rss_mib'] for x in cpu),limits=metrics.get('reassembly_limit',0),evictions=metrics.get('reassembly_pressure_evictions',0),pending_peak=metrics.get('reassembly_peak_pending_packets'),bytes_peak=metrics.get('reassembly_peak_memory_bytes'),reassembly_completed=metrics.get('reassembly_completed'),timeouts=metrics.get('reassembly_timeout',0)))
print(json.dumps(rows,indent=2))
