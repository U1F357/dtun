#!/usr/bin/env python3
"""Summarize actual outer IPv4 Mbps, per direction, in Unix-second bins.
Works with tcpdump -s 128; lengths come from original IPv4/UDP headers."""
import struct,json,sys,collections
bins=collections.defaultdict(lambda:collections.defaultdict(int));max_udp=0;frags=0;count=0
with open(sys.argv[1],'rb') as f:
 h=f.read(24);endian='<' if h[:4]==b'\xd4\xc3\xb2\xa1' else '>';link=struct.unpack(endian+'I',h[20:])[0];off={1:14,113:16,276:20,12:0,101:0}[link]
 while True:
  h=f.read(16)
  if not h:break
  sec,usec,n,orig=struct.unpack(endian+'IIII',h);p=f.read(n)[off:]
  if len(p)<28 or p[0]>>4!=4 or p[9]!=17:continue
  ihl=(p[0]&15)*4;count+=1;size=struct.unpack('!H',p[2:4])[0];ulen=struct.unpack('!H',p[ihl+4:ihl+6])[0]-8;max_udp=max(max_udp,ulen)
  frags+=bool(struct.unpack('!H',p[6:8])[0]&0x3fff)
  src='.'.join(map(str,p[12:16]));bins[src][sec]+=size
result={'packets':count,'max_udp_payload':max_udp,'outer_ip_fragments':frags,'directions':{}}
for src,rows in bins.items():
 rates={str(k):round(v*8/1e6,4) for k,v in sorted(rows.items())}
 result['directions'][src]={'max_1s_mbps':max(rates.values()),'bins_mbps':rates}
print(json.dumps(result,indent=2))
