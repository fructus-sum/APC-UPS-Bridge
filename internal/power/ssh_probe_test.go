package power

import (
 "context"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestSSHProbeIsNonDestructive(t *testing.T){
 dir:=t.TempDir()
 script:=filepath.Join(dir,"ssh")
 argsFile:=filepath.Join(dir,"args")
 body:="#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsFile+"\necho ROOT_OK\n"
 if err:=os.WriteFile(script,[]byte(body),0700);err!=nil{t.Fatal(err)}
 d:=Device{Host:"192.168.10.5",SSHUser:"root",SSHPort:22}
 result:=(LinuxExecutor{SSHPath:script}).TestSSH(context.Background(),d)
 if result.Status!="ready"{t.Fatalf("%+v",result)}
 args,e:=os.ReadFile(argsFile);if e!=nil{t.Fatal(e)}
 captured:=string(args)
 for _,want:=range []string{"StrictHostKeyChecking=yes","BatchMode=yes","192.168.10.5","command -v shutdown"}{
  if !strings.Contains(captured,want){t.Fatalf("missing %s: %s",want,captured)}
 }
 if strings.Contains(captured,"shutdown -h now"){t.Fatalf("shutdown command must not be run: %s",captured)}
}
func TestSSHProbeErrorDoesNotLeak(t *testing.T){
 d:=Device{Host:"example.invalid",SSHUser:"admin",SSHPort:2222}
 result:=(LinuxExecutor{SSHPath:"/not/a/real/ssh"}).TestSSH(context.Background(),d)
 if result.Status!="error"||strings.Contains(result.Message,"example.invalid"){t.Fatalf("%+v",result)}
}
