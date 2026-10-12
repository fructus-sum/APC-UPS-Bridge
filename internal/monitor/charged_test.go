package monitor

import (
 "context"
 "testing"
)

func TestFullChargeEventOnlyAfterObservedRecharge(t *testing.T){
 src:=&source{samples:[]sample{
 {"ONLINE",pct(100),nil},{"ONBATT",pct(80),nil},{"ONLINE",pct(81),nil},
 {"ONLINE",pct(99),nil},{"ONLINE",pct(100),nil},{"ONLINE",pct(100),nil},
 }}
 rec:=&recorder{}
 mon,err:=New(src,rec,20);if err!=nil{t.Fatal(err)}
 for range src.samples{if err:=mon.Poll(context.Background());err!=nil{t.Fatal(err)}}
 count:=0
 for _,e:=range rec.items{if e.Type=="battery_fully_charged"{count++;if e.Details["battery_percent"]!=100.0{t.Fatalf("event must record actual charge: %+v",e.Details)}}}
 if count!=1{t.Fatalf("expected one full recharge marker, got %d in %+v",count,rec.items)}
}
