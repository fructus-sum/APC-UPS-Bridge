package hostsettings

import (
 "bufio"
 "bytes"
 "errors"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strconv"
 "strings"
 "syscall"
 "time"
)

const SocketPath="/run/apc-ups-bridge-host-settings.sock"
const ConfigPath="/etc/apcupsd/apcupsd.conf"
const ManagedMarker="/etc/apcupsd/.apc-ups-bridge-managed"
type Settings struct {
 Managed bool `json:"managed"`
 BatteryPercent int `json:"battery_percent"`
 RuntimeMinutes int `json:"runtime_minutes"`
}
type Result struct {
 Applied bool `json:"applied"`
 Message string `json:"message,omitempty"`
 Error string `json:"error,omitempty"`
}
func (s Settings) Validate() error {
 if !s.Managed{return nil}
 if s.BatteryPercent < 5 || s.BatteryPercent > 95 { return fmt.Errorf("battery shutdown threshold must be between 5 and 95 percent") }
 if s.RuntimeMinutes < 0 || s.RuntimeMinutes > 120 { return fmt.Errorf("runtime shutdown threshold must be between 0 and 120 minutes") }
 return nil
}
func Apply(s Settings) Result {
 if err:=s.Validate();err!=nil{return Result{Error:err.Error()}}
 if !s.Managed{
  if os.Geteuid()!=0{return Result{Error:"The privileged configuration service is unavailable"}}
  if err:=os.Remove(ManagedMarker);err!=nil&&!os.IsNotExist(err){return Result{Error:"Could not change management mode"}}
  return Result{Message:"Web management disabled. Current apcupsd thresholds remain unchanged."}
 }
 if os.Geteuid()!=0{return Result{Error:"The privileged configuration service is unavailable"}}
 if err:=verifyOnline();err!=nil{return Result{Error:err.Error()}}
 if err:=applyConfig(ConfigPath,s,func()error{return exec.Command("/usr/bin/systemctl","restart","apcupsd.service").Run()});err!=nil{return Result{Error:err.Error()}}
 if err:=os.WriteFile(ManagedMarker,[]byte("managed by APC UPS Bridge\\n"),0600);err!=nil{return Result{Error:"Thresholds applied, but the web-management marker could not be saved"}}
 return Result{Applied:true,Message:"Pi shutdown thresholds applied. Verify apcupsd status."}
}
func verifyOnline()error{
 var output []byte;var err error
 for _,path:=range []string{"/usr/sbin/apcaccess","/usr/bin/apcaccess"} {
  if _,e:=os.Stat(path);e==nil{output,err=exec.Command(path,"status").Output();break}
 }
 if err!=nil||len(output)==0{return errors.New("Cannot confirm live UPS status; settings unchanged")}
 scan:=bufio.NewScanner(bytes.NewReader(output))
 for scan.Scan() {
  key,val,ok:=strings.Cut(scan.Text(),":")
  if ok&&strings.EqualFold(strings.TrimSpace(key),"STATUS"){
   if strings.EqualFold(strings.TrimSpace(val),"ONLINE"){return nil}
   return errors.New("UPS is not confirmed ONLINE; changes are blocked")
  }
 }
 return errors.New("UPS status missing; settings unchanged")
}
func replaceThresholds(data []byte,s Settings)[]byte{
 values:=map[string]string{"BATTERYLEVEL":strconv.Itoa(s.BatteryPercent),"MINUTES":strconv.Itoa(s.RuntimeMinutes),"TIMEOUT":"0"}
 seen:=map[string]bool{}
 lines:=strings.Split(strings.TrimSuffix(string(data),"\n"),"\n")
 out:=make([]string,0,len(lines)+3)
 for _,line:=range lines{
  fields:=strings.Fields(line)
  if len(fields)>0{
   key:=strings.ToUpper(fields[0])
   if v,ok:=values[key];ok{
    if !seen[key]{out=append(out,key+" "+v);seen[key]=true}
    continue
   }
  }
  out=append(out,line)
 }
 for _,k:=range []string{"BATTERYLEVEL","MINUTES","TIMEOUT"}{if !seen[k]{out=append(out,k+" "+values[k])}}
 return []byte(strings.Join(out,"\n")+"\n")
}
func applyConfig(path string,s Settings,restart func()error)error{
 fi,err:=os.Lstat(path);if err!=nil||!fi.Mode().IsRegular(){return errors.New("Unsafe or missing apcupsd configuration")}
 original,err:=os.ReadFile(path);if err!=nil{return errors.New("Cannot read apcupsd configuration")}
 newContent:=replaceThresholds(original,s)
 if bytes.Equal(original,newContent){return nil}
 backup:=path+".bridge-backup-"+time.Now().UTC().Format("20060102T150405.000000000")
 if err:=os.WriteFile(backup,original,0600);err!=nil{return errors.New("Cannot back up apcupsd configuration")}
 if err:=atomicReplace(path,newContent,fi);err!=nil{return errors.New("Cannot write apcupsd configuration")}
 if err:=restart();err!=nil{
  _=atomicReplace(path,original,fi)
  _=restart()
  return errors.New("apcupsd restart failed; previous configuration restored where possible")
 }
 return nil
}
func atomicReplace(path string,data []byte,original os.FileInfo)error{
 f,err:=os.CreateTemp(filepath.Dir(path),".apc-bridge-*");if err!=nil{return err}
 defer os.Remove(f.Name())
 if err:=f.Chmod(original.Mode().Perm());err!=nil{f.Close();return err}
 if sys,ok:=original.Sys().(*syscall.Stat_t);ok{
  if err:=f.Chown(int(sys.Uid),int(sys.Gid));err!=nil{f.Close();return err}
 }
 if _,err:=f.Write(data);err!=nil{f.Close();return err}
 if err:=f.Sync();err!=nil{f.Close();return err}
 if err:=f.Close();err!=nil{return err}
 if err:=os.Rename(f.Name(),path);err!=nil{return err}
 dir,err:=os.Open(filepath.Dir(path));if err!=nil{return err}
 defer dir.Close()
 return dir.Sync()
}
