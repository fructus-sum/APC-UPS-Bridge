package hostsettings

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestReplaceThresholds(t *testing.T) {
 input:=[]byte("# keep comment\nBATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 120\nNETSERVER on\n")
 out:=string(replaceThresholds(input,Settings{Managed:true,BatteryPercent:12,RuntimeMinutes:4}))
 for _,expected:=range []string{"BATTERYLEVEL 12\n","MINUTES 4\n","TIMEOUT 0\n","NETSERVER on\n","# keep comment\n"}{
  if !strings.Contains(out,expected){t.Fatalf("missing %q in %q",expected,out)}
 }
 if strings.Count(out,"BATTERYLEVEL")!=1{t.Fatal("duplicate battery key")}
}
func TestApplyConfigBackupAndRollback(t *testing.T){
 dir:=t.TempDir();file:=filepath.Join(dir,"apcupsd.conf")
 original:=[]byte("BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n")
 if err:=os.WriteFile(file,original,0600);err!=nil{t.Fatal(err)}
 settings:=Settings{Managed:true,BatteryPercent:15,RuntimeMinutes:5}
 calls:=0
 if err:=applyConfig(file,settings,func()error{calls++;return nil});err!=nil{t.Fatal(err)}
 got,_:=os.ReadFile(file)
 if !strings.Contains(string(got),"BATTERYLEVEL 15"){t.Fatalf("%s",got)}
 matches,_:=filepath.Glob(file+".bridge-backup-*")
 if len(matches)!=1{t.Fatalf("expected a backup, got %v",matches)}
 b,_:=os.ReadFile(matches[0]);if string(b)!=string(original){t.Fatal("backup mismatch")}
 if calls!=1{t.Fatalf("restart called %d times",calls)}
}
