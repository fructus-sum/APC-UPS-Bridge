package history

import (
 "os"
 "path/filepath"
 "testing"
 "time"

 "github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
 "github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

func val(v float64)*float64{return &v}
func TestBatteryRuntimeSamplingAcrossRestart(t *testing.T){
 dir:=t.TempDir()
 now:=time.Now().UTC().Add(-2*time.Hour).Truncate(Interval).Add(time.Minute)
 s:=New(dir)
 for _,snap:=range []apc.Snapshot{
  {ObservedAt:now,Power:apc.OnBattery,BatteryPercent:val(34),RuntimeMinutes:val(7.5)},
  {ObservedAt:now.Add(Interval),Power:apc.Online,BatteryPercent:val(40),RuntimeMinutes:val(12)},
 }{
  if err:=s.Record(&snap);err!=nil{t.Fatal(err)}
 }
 reloaded:=New(dir)
 if err:=reloaded.Record(&apc.Snapshot{ObservedAt:now.Add(Interval+time.Minute),Power:apc.Online,BatteryPercent:val(99)});err!=nil{t.Fatal(err)}
 got,err:=reloaded.Read(1,now.Add(2*Interval),"")
 if err!=nil{t.Fatal(err)}
 if got.Samples!=2{t.Fatalf("dedupe failed: %d samples",got.Samples)}
 var found bool
 for _,p:=range got.Points{
  if p.Time.Equal(now.Truncate(Interval)){
   found=true
   if p.BatteryPercent==nil||*p.BatteryPercent!=34||p.RuntimeMinutes==nil||*p.RuntimeMinutes!=7.5||p.Power!=apc.OnBattery{t.Fatalf("lost battery history: %+v",p)}
  }
 }
 if !found{t.Fatal("missing first reading")}
}
func TestEventBackfillUsesOnlyRecordedReadings(t *testing.T){
 readings:=t.TempDir()
 logs:=t.TempDir()
 now:=time.Now().UTC().Truncate(Interval)
 writer:=events.FileWriter{Dir:logs}
 starts:=now.Add(-2*time.Hour)
 for _,e:=range []events.Event{
  {Time:starts,Type:"power_lost",Details:map[string]any{"battery_percent":76.0,"runtime_minutes":17.0}},
  {Time:starts.Add(Interval),Type:"low_battery",Details:map[string]any{"battery_percent":"invalid","runtime_minutes":-12.0}},
  {Time:starts.Add(time.Hour),Type:"power_restored",Details:map[string]any{"battery_percent":20.0}},
  {Time:starts.Add(90*time.Minute),Type:"battery_fully_charged",Details:map[string]any{"battery_percent":100.0}},
 }{
  if err:=writer.Write(e);err!=nil{t.Fatal(err)}
 }
 got,err:=New(readings).Read(1,now,logs)
 if err!=nil{t.Fatal(err)}
 if got.Samples!=0||len(got.Events)!=4{t.Fatalf("bad event results: %+v",got)}
 check:=func(t0 time.Time)*Bucket{t.Helper();for i:=range got.Points{if got.Points[i].Time.Equal(t0){return &got.Points[i]}};t.Fatalf("missing %s",t0);return nil}
 p:=check(starts)
 if p.BatteryPercent==nil||*p.BatteryPercent!=76||p.RuntimeMinutes==nil||*p.RuntimeMinutes!=17||p.Observed||!p.BatteryFromEvent||!p.RuntimeFromEvent{t.Fatalf("bad backfill: %+v",p)}
 if check(starts.Add(Interval)).BatteryPercent!=nil||check(starts.Add(Interval)).RuntimeMinutes!=nil{t.Fatal("invalid event values accepted")}
 if check(starts.Add(30*time.Minute)).BatteryPercent!=nil{t.Fatal("interpolated a missing measurement")}
 if q:=check(starts.Add(90*time.Minute));q.BatteryPercent==nil||*q.BatteryPercent!=100{t.Fatalf("missing actual full-charge event: %+v",q)}
}
func TestOldSampleSchemaAndEventMeasurementPrecedence(t *testing.T){
 dir:=t.TempDir();logs:=t.TempDir()
 now:=time.Now().UTC().Add(-time.Hour).Truncate(Interval)
 // Old JSON samples do not have battery/runtime fields.
 if err:=os.WriteFile(filepath.Join(dir,filename(now)),[]byte(`{"time":"`+now.Format(time.RFC3339)+`","input_volts":230,"load_percent":25}`+"\n"),0600);err!=nil{t.Fatal(err)}
 if err:=events.FileWriter{Dir:logs}.Write(events.Event{Time:now,Type:"power_lost",Details:map[string]any{"battery_percent":51.0}});err!=nil{t.Fatal(err)}
 got,err:=New(dir).Read(1,now,logs);if err!=nil{t.Fatal(err)}
 for _,p:=range got.Points{if p.Time.Equal(now){if !p.Observed||p.BatteryPercent==nil||*p.BatteryPercent!=51||!p.BatteryFromEvent{t.Fatalf("old format incompatible: %+v",p)};return}}
 t.Fatal("no slot found")
}
