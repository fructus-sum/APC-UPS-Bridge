package power

import (
 "context"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestSSHProbeNeverRequestsRemoteCommand(t *testing.T) {
 dir:=t.TempDir()
 script:=filepath.Join(dir,"ssh")
 argsFile:=filepath.Join(dir,"args")
 body:="#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsFile+"\necho 'Authenticated to 192.168.10.5 ([192.168.10.5]:22) using \"publickey\".' >&2\n"
 if err:=os.WriteFile(script,[]byte(body),0700);err!=nil{t.Fatal(err)}
 d:=Device{Host:"192.168.10.5",SSHUser:"root",SSHPort:22}
 result:=(LinuxExecutor{SSHPath:script}).TestSSH(context.Background(),d)
 if result.Status!="ready"||!strings.Contains(result.Message,"NOT verified"){t.Fatalf("%+v",result)}
 args,e:=os.ReadFile(argsFile);if e!=nil{t.Fatal(e)}
 captured:=string(args)
 for _,want:=range []string{"-N","-T","StrictHostKeyChecking=yes","BatchMode=yes","PreferredAuthentications=publickey","ClearAllForwardings=yes","192.168.10.5"} {
  if !strings.Contains(captured,want){t.Fatalf("missing %q: %s",want,captured)}
 }
 for _,want:=range []string{"ControlMaster=no","ControlPath=none"} {
  if !strings.Contains(captured,want){t.Fatalf("missing %q: %s",want,captured)}
 }
 if lines:=strings.Split(strings.TrimSpace(captured),"\n");lines[len(lines)-1]!="root@192.168.10.5"{
  t.Fatalf("destination is not the final argument: %s",captured)
 }
 if strings.Contains(captured,"shutdown")||strings.Contains(captured,"sh -c")||strings.Contains(captured,"sudo"){
  t.Fatalf("a remote command was requested: %s",captured)
 }
}
func TestSSHProbeErrorDoesNotLeak(t *testing.T){
 d:=Device{Host:"example.invalid",SSHUser:"admin",SSHPort:2222}
 result:=(LinuxExecutor{SSHPath:"/not/a/real/ssh"}).TestSSH(context.Background(),d)
 if result.Status!="error"||strings.Contains(result.Message,"example.invalid"){t.Fatalf("%+v",result)}
}
