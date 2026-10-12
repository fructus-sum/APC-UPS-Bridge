package web

import (
 "context"
 "net/http"
 "strings"
 "testing"
 "time"

 "github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

func TestDashboardCollapsedSystemDetailsAndFirmwareLocation(t *testing.T) {
 h,a:=setupWeb(t)
 token,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 dash:=request(h,"GET","/","",cookie,"")
 body:=dash.Body.String()
 for _,part:=range []string{`id="ups-model-heading"`,`id="model"`,`<details id="system-information"`,`System information`,`id="dashboard-remotes"`} {
  if !strings.Contains(body,part){t.Errorf("missing dashboard section: %s",part)}
 }
 for _,part:=range []string{`id="health-battery-voltage"`,`id="health-test-result"`,`id="battery-test-guide"`,`id="battery-test-instructions" hidden`} {if !strings.Contains(body,part){t.Errorf("missing battery health: %s",part)}}
 if strings.Contains(body,`id="firmware-installed"`){t.Fatal("firmware still on dashboard")}
 settings:=request(h,"GET","/settings","",cookie,"")
 if !strings.Contains(settings.Body.String(),`id="firmware-installed"`){t.Fatal("firmware not moved to settings")}
 powerPage:=request(h,"GET","/power","",cookie,"")
 if !strings.Contains(powerPage.Body.String(),`id="device-list"`){t.Fatal("missing remote device editor")}
 js:=request(h,"GET","/assets/app.js","",cookie,"")
 for _,part:=range []string{"device-details","details.open=!data.id","refreshDeviceStatuses","refreshFirmware();setInterval(refreshFirmware"} {
  if !strings.Contains(js.Body.String(),part){t.Errorf("missing browser wiring: %s",part)}
 }
}
func TestDeviceStatusRequiresAuthenticationAndIsReadOnly(t *testing.T) {
 h,a:=setupWeb(t)
 if w:=request(h,"GET","/api/power/devices/status","",nil,"");w.Code!=http.StatusUnauthorized{t.Fatalf("unauthorized access: %d",w.Code)}
 token,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 if w:=request(h,"GET","/api/power/devices/status","",cookie,"");w.Code!=http.StatusServiceUnavailable{t.Fatalf("missing controller status: %d",w.Code)}
 if w:=request(h,"POST","/api/power/devices/status","",cookie,"");w.Code!=http.StatusMethodNotAllowed{t.Fatalf("write access: %d",w.Code)}
}
func TestReachabilityRetainsFriendlyNamesAndOrder(t *testing.T) {
 devices:=[]power.Device{{ID:"nas",Name:"Family NAS"},{ID:"node2",Name:"Office server"}}
 res:=probeSavedDevices(context.Background(),devices,func(_ context.Context,d power.Device)bool{return d.ID=="nas"})
 if len(res)!=2||res[0].Name!="Family NAS"||res[0].Status!="online"||res[1].Name!="Office server"||res[1].Status!="offline"{t.Fatalf("unexpected view: %+v",res)}
 for _,d:=range res{if d.CheckedAt.IsZero()||time.Since(d.CheckedAt)>time.Minute{t.Fatalf("missing timestamp: %+v",d)}}
}
