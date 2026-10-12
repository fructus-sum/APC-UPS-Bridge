package notify

import (
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

func TestSecurityWarningThrottleAndNoAddress(t *testing.T) {
 store,err:=OpenConfig(filepath.Join(t.TempDir(),"email.json"));if err!=nil{t.Fatal(err)}
 if err:=store.Save(Config{Enabled:true,Host:"smtp.example.com",Port:587,Security:"starttls",Username:"from@example.com",Password:"secret",From:"from@example.com",To:"to@example.com"});err!=nil{t.Fatal(err)}
 sender:=&fakeSender{}
 e:=&Engine{store:store,sender:sender,now:time.Now}
 e.SendSecurityWarning()
 e.SendSecurityWarning()
 if len(sender.messages)!=1{t.Fatalf("expected throttled single email, got %d",len(sender.messages))}
 if strings.Contains(sender.messages[0].body,"192.0.2") {t.Fatal("email leaked an address")}
}
func TestFinalReportUsesFriendlyNamesNotAddresses(t *testing.T) {
 dir:=t.TempDir()
 if err:=os.MkdirAll(dir,0700);err!=nil{t.Fatal(err)}
 now:=time.Date(2026,10,12,15,0,0,0,time.UTC)
 w:=events.FileWriter{Dir:dir}
 if err:=w.Write(events.Event{Time:now.Add(-time.Hour),Type:"remote_shutdown",Level:"info",Details:map[string]any{"host":"192.0.2.50","name":"Living room NAS","battery_percent":37.0}});err!=nil{t.Fatal(err)}
 e:=&Engine{logs:dir}
 report:=&Incident{ID:"outage-123",CreatedAt:now.Add(-2*time.Hour),Targets:[]Target{{ID:"nas",Name:"Living room NAS",Host:"192.0.2.50",Port:22}},Periods:[]Period{{StartedAt:now.Add(-2*time.Hour),EndedAt:now.Add(-time.Hour)}}}
 _,body:=e.summary(report,now)
 if !strings.Contains(body,"Living room NAS"){t.Fatal("friendly name missing from report")}
 if strings.Contains(body,"192.0.2.50")||strings.Contains(body,"host")&&strings.Contains(body,"192.0.2"){t.Fatalf("address leaked in report: %s",body)}
 if !strings.Contains(body,"battery_percent"){t.Fatal("useful metric missing from report")}
}
