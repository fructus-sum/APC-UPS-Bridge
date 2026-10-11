package web

import (
 "bufio"
 "context"
 "encoding/json"
 "fmt"
 "net"
 "time"
 "github.com/fructus-sum/APC-UPS-Bridge/internal/hostsettings"
 "os"
 "strconv"
 "strings"

 "github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

const apcupsdConfigPath = "/etc/apcupsd/apcupsd.conf"

// hostShutdownSettings reads the effective configured apcupsd thresholds.
// Do not invent defaults when a setting or config file cannot be read.
func hostShutdownSettings() map[string]any {
 _,marked:=os.Stat(hostsettings.ManagedMarker)
 result:=map[string]any{"managed":marked==nil,"source":apcupsdConfigPath,"available":false,"battery_percent":nil,"runtime_minutes":nil,"timeout_seconds":nil}
 f,err:=os.Open(apcupsdConfigPath);if err!=nil{return result};defer f.Close()
 result["available"]=true
 s:=bufio.NewScanner(f)
 for s.Scan(){
  line:=strings.TrimSpace(s.Text())
  if line==""||strings.HasPrefix(line,"#"){continue}
  parts:=strings.Fields(line)
  if len(parts)<2{continue}
  key:=strings.ToUpper(parts[0])
  switch key{
  case "BATTERYLEVEL","MINUTES","TIMEOUT":
   n,e:=strconv.Atoi(parts[1]);if e!=nil||n<0{continue}
   switch key{case "BATTERYLEVEL":result["battery_percent"]=n
   case "MINUTES":result["runtime_minutes"]=n
   case "TIMEOUT":result["timeout_seconds"]=n}
  }
 }
 if s.Err()!=nil{result["available"]=false}
 return result
}

func (h *Handler) testSavedSSH(ctx context.Context,id string) (power.SSHProbeResult,bool) {
 if h.Power==nil{return power.SSHProbeResult{},false}
 for _,d:=range h.Power.Config().Devices{
  if d.ID==id {return (power.LinuxExecutor{}).TestSSH(ctx,d),true}
 }
 return power.SSHProbeResult{},false
}

func applyHostSettings(ctx context.Context,settings hostsettings.Settings)(hostsettings.Result,error){
 c,err:=(&net.Dialer{Timeout:3*time.Second}).DialContext(ctx,"unix",hostsettings.SocketPath)
 if err!=nil{return hostsettings.Result{},fmt.Errorf("check that the host settings socket is installed and enabled")}
 defer c.Close()
 deadline:=time.Now().Add(50*time.Second)
 if d,ok:=ctx.Deadline();ok&&d.Before(deadline){deadline=d}
 _=c.SetDeadline(deadline)
 if err:=json.NewEncoder(c).Encode(settings);err!=nil{return hostsettings.Result{},fmt.Errorf("cannot send host settings")}
 var reply hostsettings.Result
 if err:=json.NewDecoder(c).Decode(&reply);err!=nil{return hostsettings.Result{},fmt.Errorf("no response from host settings service; the change may or may not have been applied, refresh to check the current Pi thresholds")}
 return reply,nil
}
