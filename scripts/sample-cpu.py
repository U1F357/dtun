#!/usr/bin/env python3
"""Process CPU: 100% means one logical CPU. Reads /proc, not iperf statistics."""
import argparse,time,os,json
p=argparse.ArgumentParser();p.add_argument('--pid',type=int,required=True);p.add_argument('--seconds',type=int,default=45);p.add_argument('--out',required=True);a=p.parse_args()
hz=os.sysconf('SC_CLK_TCK');page=os.sysconf('SC_PAGE_SIZE');cpus=os.cpu_count();samples=[]
def read():
 s=open('/proc/%d/stat'%a.pid).read().rsplit(')',1)[1].split()
 return (int(s[11])+int(s[12]))/hz,int(s[21])*page
cpu,rss=read();last=time.monotonic()
for _ in range(a.seconds):
 time.sleep(1);now=time.monotonic();value,rss=read()
 samples.append(dict(time=time.time(),cpu_percent=(value-cpu)/(now-last)*100,rss_mib=rss/1048576))
 cpu=value;last=now
with open(a.out,'w') as f:json.dump(dict(pid=a.pid,logical_cpus=cpus,cpu_percent_definition='100% = one logical CPU',samples=samples),f,indent=2)
