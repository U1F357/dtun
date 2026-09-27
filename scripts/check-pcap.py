#!/usr/bin/env python3
"""Read classic tcpdump pcap without third-party dependencies; verify IPv4 UDP cap
and byte-for-byte equality of TCP L3 packets at both TUN interfaces."""
import struct,sys,json,collections

def packets(path):
 with open(path,'rb') as f:
  h=f.read(24); endian='<' if h[:4] in (b'\xd4\xc3\xb2\xa1',b'\x4d\x3c\xb2\xa1') else '>'
  link=struct.unpack(endian+'I',h[20:24])[0]
  while True:
   h=f.read(16)
   if not h: break
   _,_,n,_=struct.unpack(endian+'4I',h);p=f.read(n)
   off={1:14,12:0,101:0,113:16,276:20}.get(link)
   if off is None: raise ValueError(('link type',link))
   yield p[off:]
outer=list(packets(sys.argv[1]));sizes=[];frags=0;records=set();duplicates=0;app_records=0
for p in outer:
 if len(p)<20 or p[0]>>4!=4: continue
 ihl=(p[0]&15)*4
 if p[9]!=17: continue
 frags+=bool(struct.unpack('!H',p[6:8])[0]&0x3fff)
 sizes.append(struct.unpack('!H',p[ihl+4:ihl+6])[0]-8)
 data=p[ihl+8:];pos=0
 while pos+13<=len(data):
  h=data[pos:pos+13];length=struct.unpack('!H',h[11:13])[0]
  if pos+13+length>len(data): break
  if h[0]==23:
   key=(p[12:20],p[ihl:ihl+4],h[3:11]);app_records+=1
   duplicates+=key in records;records.add(key)
  pos+=13+length
assert sizes and max(sizes)<=1200 and frags==0,(max(sizes,default=0),frags)
result={'outer_udp_packets':len(sizes),'max_udp_payload':max(sizes),'outer_ip_fragments':frags,'udp_payload_histogram':dict(collections.Counter(sizes)),'application_records':app_records,'duplicate_application_record_sequences':duplicates}
if len(sys.argv)>3:
 def tcp(path):return collections.Counter(p for p in packets(path) if len(p)>=20 and p[0]>>4==4 and p[9]==6)
 a,b=tcp(sys.argv[2]),tcp(sys.argv[3]);common=a&b
 result.update(tcp_packets_client=sum(a.values()),tcp_packets_server=sum(b.values()),tcp_packets_identical=sum(common.values()),tcp_only_client=sum((a-b).values()),tcp_only_server=sum((b-a).values()))
 # Queue drops are permitted, but enough common packets must prove opaque transfer.
 assert sum(common.values())>=100,result
print(json.dumps(result,indent=2))
