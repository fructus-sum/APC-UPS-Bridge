package main

import (
 "encoding/json"
 "net"
 "os"
 "time"
 "github.com/fructus-sum/APC-UPS-Bridge/internal/hostsettings"
)

// Started only by the root-owned systemd unit with a pre-opened Unix socket.
func main() {
 file:=os.NewFile(3,"systemd-socket")
 if file==nil{return}
 listener,err:=net.FileListener(file);if err!=nil{return}
 defer listener.Close()
 for {
  if l,ok:=listener.(*net.UnixListener);ok{_ =l.SetDeadline(time.Now().Add(5*time.Minute))}
  conn,err:=listener.Accept();if err!=nil{return}
  func(){
   defer conn.Close()
   _=conn.SetReadDeadline(time.Now().Add(5*time.Second))
   var s hostsettings.Settings
   decoder:=json.NewDecoder(conn)
   decoder.DisallowUnknownFields()
   result:=hostsettings.Result{}
   if err:=decoder.Decode(&s);err!=nil{result.Error="Invalid request"}else{result=hostsettings.Apply(s)}
   _=conn.SetWriteDeadline(time.Now().Add(5*time.Second))
   _=json.NewEncoder(conn).Encode(result)
  }()
 }
}
