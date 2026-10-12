package web

import (
 "net/http"
 "strings"
 "testing"
)

func TestThirdHistoryChartIsActuallyWired(t *testing.T){
 h,a:=setupWeb(t)
 tok,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:tok}
 page:=request(h,"GET","/history","",cookie,"")
 if page.Code!=200||!strings.Contains(page.Body.String(),`id="battery-chart"`){t.Fatalf("missing third chart in page: %d",page.Code)}
 js:=request(h,"GET","/assets/history.js","",cookie,"")
 if js.Code!=200||!strings.Contains(js.Body.String(),"drawBattery(report)"){t.Fatalf("third chart not invoked: %d",js.Code)}
}
