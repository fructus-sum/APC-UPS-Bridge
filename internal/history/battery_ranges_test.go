package history

import (
 "testing"
 "time"
 "github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

func TestLongBatteryRangesIncludeOldEventsButNotSyntheticSamples(t *testing.T) {
 sampleDir:=t.TempDir(); eventDir:=t.TempDir()
 now:=time.Now().UTC().Truncate(Interval)
 old:=now.Add(-300*24*time.Hour)
 w:=events.FileWriter{Dir:eventDir}
 if err:=w.Write(events.Event{Time:old,Type:"power_lost",Details:map[string]any{"battery_percent":56.0,"runtime_minutes":14.0}});err!=nil{t.Fatal(err)}
 s:=New(sampleDir)
 for _,days:=range []int{30,90,180,365}{
  got,err:=s.ReadBattery(days,now,eventDir);if err!=nil{t.Fatal(err)}
  if len(got.Points)!=30*24*4+1{t.Fatalf("%d-day view should include only recent sample slots, got %d",days,len(got.Points))}
  if !got.From.Equal(now.Add(-time.Duration(days)*24*time.Hour)){t.Fatalf("wrong axis for %d",days)}
  want:=0;if days==365{want=1}
  if len(got.Events)!=want{t.Fatalf("%d-day range events=%d want=%d",days,len(got.Events),want)}
  if want==1{if got.Events[0].Details["battery_percent"]!=56.0{t.Fatal("old genuine event missing")}}
  if got.Samples!=0{t.Fatal("event-only history fabricated samples")}
 }
 for _,days:=range []int{0,1,14,31,366,-1}{
  if _,err:=s.ReadBattery(days,now,eventDir);err==nil{t.Fatalf("invalid range accepted: %d",days)}
 }
}
