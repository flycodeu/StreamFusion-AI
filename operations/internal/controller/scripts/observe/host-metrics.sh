export LC_ALL=C
printf "SFOPS_METRICS_V1\n"
printf "HOST\t"; hostname
printf "KERNEL\t"; uname -r
awk -F= '/^PRETTY_NAME=/ {gsub(/"/,"",$2); print "OS\t" $2}' /etc/os-release 2>/dev/null
printf "CORES\t"; getconf _NPROCESSORS_ONLN
awk -F: '/^model name/ {sub(/^ +/,"",$2); print "MODEL\t" $2; exit}' /proc/cpuinfo
sample() {
  awk -v phase="$1" '/^cpu / {total=0; for(i=2;i<=9;i++) total+=$i; printf("CPU%s\t%.0f\t%.0f\n",phase,total,$5+$6)}' /proc/stat
  awk -v phase="$1" '{print "TIME" phase "\t" $1}' /proc/uptime
  awk -v phase="$1" 'NR>2 {sub(/^ +/,""); split($0,a,/[: ]+/); if(a[1]!="lo") print "NET" phase "\t" a[1] "\t" a[2] "\t" a[10]}' /proc/net/dev
}
for nic in /sys/class/net/*; do
  if [ -e "$nic/device" ]; then printf "PHYSICAL\t%s\n" "${nic##*/}"; fi
done
sample 0
sleep 1
sample 1
awk '/^MemTotal:/ {printf("MEMTOTAL\t%.0f\n",$2*1024)} /^MemAvailable:/ {printf("MEMAVAILABLE\t%.0f\n",$2*1024)}' /proc/meminfo
awk '{print "LOAD\t" $1 "\t" $2 "\t" $3}' /proc/loadavg
df -Pk / 2>/dev/null | awk 'NR==2 {printf("DISK\t%.0f\t%.0f\n",$2*1024,$3*1024)}'
