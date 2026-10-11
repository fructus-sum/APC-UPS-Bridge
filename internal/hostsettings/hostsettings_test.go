package hostsettings

import "testing"

func TestSettingsValidation(t *testing.T) {
 for _,s:=range []Settings{{Managed:true,BatteryPercent:10,RuntimeMinutes:3},{Managed:true,BatteryPercent:95,RuntimeMinutes:0},{Managed:true,BatteryPercent:10,TimeoutMinutes:0},{Managed:true,BatteryPercent:10,TimeoutMinutes:120}} {
  if err:=s.Validate();err!=nil{t.Fatalf("valid settings rejected: %v",err)}
 }
 for _,s:=range []Settings{{Managed:true,BatteryPercent:4,RuntimeMinutes:3},{Managed:true,BatteryPercent:96},{Managed:true,BatteryPercent:10,RuntimeMinutes:-1},{Managed:true,BatteryPercent:10,RuntimeMinutes:121},{Managed:true,BatteryPercent:10,TimeoutMinutes:-1},{Managed:true,BatteryPercent:10,TimeoutMinutes:121}} {
  if err:=s.Validate();err==nil{t.Fatalf("invalid settings allowed: %+v",s)}
 }
}
