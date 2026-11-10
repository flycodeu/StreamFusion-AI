export LC_ALL=C
printf "SFOPS_METRICS_V1\n"
printf "HOST\t"; hostname
printf "KERNEL\t"; uname -r
printf "ARCH\t"; uname -m
awk -F= '/^PRETTY_NAME=/ {gsub(/"/,"",$2); print "OS\t" substr($2,1,256)}' /etc/os-release 2>/dev/null
printf "CORES\t"; getconf _NPROCESSORS_ONLN
awk -F: '/^model name/ {sub(/^ +/,"",$2); print "MODEL\t" substr($2,1,512); exit}' /proc/cpuinfo
awk '/^btime / {print "BOOT\t" $2}' /proc/stat
sample() {
  awk -v phase="$1" '/^cpu / {total=0; for(i=2;i<=9;i++) total+=$i; printf("CPU%s\t%.0f\t%.0f\n",phase,total,$5+$6)}' /proc/stat
  awk -v phase="$1" '{print "TIME" phase "\t" $1}' /proc/uptime
  awk -v phase="$1" 'NR>2 {sub(/^ +/,""); split($0,a,/[: ]+/); if(a[1]!="lo" && count++<64) print "NET" phase "\t" a[1] "\t" a[2] "\t" a[10]}' /proc/net/dev
}
physical_count=0
for nic in /sys/class/net/*; do
  if [ -e "$nic/device" ] && [ "$physical_count" -lt 64 ]; then
    printf "PHYSICAL\t%s\n" "${nic##*/}"
    physical_count=$((physical_count + 1))
  fi
done
if command -v ip >/dev/null 2>&1; then
  ip -o route show default 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="dev") {print "PRIMARY\t" $(i+1); exit}}'
  ip -o address show 2>/dev/null | awk '($3=="inet" || $3=="inet6") && $2!="lo" {name=$2; sub(/@.*/,"",name); if(count<64 && interfaces[name]<4) {print "NETADDR\t" name "\t" $4; count++; interfaces[name]++}}'
fi
sample 0
sleep 1
sample 1
awk '/^MemTotal:/ {printf("MEMTOTAL\t%.0f\n",$2*1024)} /^MemAvailable:/ {printf("MEMAVAILABLE\t%.0f\n",$2*1024)} /^SwapTotal:/ {printf("SWAPTOTAL\t%.0f\n",$2*1024)} /^SwapFree:/ {printf("SWAPFREE\t%.0f\n",$2*1024)}' /proc/meminfo
awk '{print "LOAD\t" $1 "\t" $2 "\t" $3}' /proc/loadavg
df -Pk / 2>/dev/null | awk 'NR==2 {printf("DISK\t%.0f\t%.0f\n",$2*1024,$3*1024)}'
if command -v ps >/dev/null 2>&1; then
  printf 'PROCESS_CPU_BASIS\tLIFETIME_AVERAGE\n'
  ps -eo pid=,user:64=,pcpu=,rss=,comm= --sort=-pcpu 2>/dev/null | awk 'NR<=20 && NF>=5 {name=$5; for(i=6;i<=NF;i++) name=name " " $i; gsub(/[[:cntrl:]]/," ",name); printf("PROCESS\t%s\t%s\t%s\t%.0f\t%s\n",$1,substr($2,1,64),$3,$4*1024,substr(name,1,128))}'
fi
