package web

import (
	"encoding/json"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryPageAndAPIProtected(t *testing.T) {
	h, auth := setupWeb(t)
	AttachHistory(h, history.New(filepath.Join(t.TempDir(), "history")))
	for _, path := range []string{"/history", "/api/history", "/api/history/battery", "/assets/history.js"} {
		w := request(h, "GET", path, "", nil, "")
		if w.Code != http.StatusSeeOther && w.Code != http.StatusUnauthorized {
			t.Fatalf("not protected: %s status %d", path, w.Code)
		}
	}
	token, _, err := auth.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "ups_bridge_session", Value: token}
	w := request(h, "GET", "/history", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "history-days") {
		t.Fatalf("page unavailable %d %s", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/history", "", cookie, "")
	if w.Code != 200 {
		t.Fatalf("API status %d %s", w.Code, w.Body.String())
	}
	var data history.Result
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Points) != 14*24*4+1 || data.IntervalSeconds != int((15*time.Minute).Seconds()) {
		t.Fatalf("bad default axis: %+v", data)
	}
	if w = request(h, "GET", "/api/history?days=abc", "", cookie, ""); w.Code != 400 {
		t.Fatalf("non-numeric range allowed %d", w.Code)
	}
	if w = request(h, "GET", "/api/history?days=31", "", cookie, ""); w.Code != 400 {
		t.Fatalf("bad range allowed %d", w.Code)
	}
	if w = request(h, "GET", "/assets/history.js", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "createElementNS") {
		t.Fatalf("missing JS %d", w.Code)
	}
}

func TestBatteryHistoryIndependentRanges(t *testing.T) {
 h,a:=setupWeb(t)
 AttachHistory(h,history.New(filepath.Join(t.TempDir(),"history")))
 token,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 for _,days:=range []string{"","30","90","180","365"}{
  suffix:="";if days!=""{suffix="?days="+days}
  w:=request(h,"GET","/api/history/battery"+suffix,"",cookie,"")
  if w.Code!=200{t.Fatalf("battery range %q HTTP %d: %s",days,w.Code,w.Body.String())}
  var out history.Result;if err:=json.Unmarshal(w.Body.Bytes(),&out);err!=nil{t.Fatal(err)}
  if out.Samples!=0 || len(out.Points)!=30*24*4+1{t.Fatalf("unexpected sparse result %+v",out)}
  if days=="" || days=="30"{if out.To.Sub(out.From)!=30*24*time.Hour{t.Fatal("wrong default")}}
 }
 for _,days:=range []string{"31","14","366","abc","-1"}{
  w:=request(h,"GET","/api/history/battery?days="+days,"",cookie,"")
  if w.Code!=400{t.Fatalf("invalid %s accepted %d",days,w.Code)}
 }
 w:=request(h,"GET","/history","",cookie,"")
 if !strings.Contains(w.Body.String(),`id="battery-history-days"`) || !strings.Contains(w.Body.String(),`value="365"`){t.Fatal("battery-only range selector missing")}
}
