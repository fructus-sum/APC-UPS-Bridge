package apc

import (
 "testing"
 "time"
)
func TestBatteryMaintenanceOptionalFields(t *testing.T) {
 text:=[]byte("STATUS: ONLINE\nBATTV: 26.7 Volts\nNOMBATTV: 24.0 Volts\nBATTDATE: 2025-05-03\nLASTSTEST: 2026-10-11 12:30:00 +0100\nSELFTEST: OK\nSTESTI: 336\n")
 got,err:=Parse(text,time.Now());if err!=nil{t.Fatal(err)}
 if got.BatteryVolts==nil||*got.BatteryVolts!=26.7||got.NominalBatteryVolts==nil||*got.NominalBatteryVolts!=24{t.Fatalf("battery voltage: %+v",got)}
 if got.BatteryDate!="2025-05-03"||got.SelfTestResult!="OK"||got.LastSelfTest==""||got.SelfTestInterval!="336"{t.Fatalf("maintenance metadata: %+v",got)}
 missing,err:=Parse([]byte("STATUS: ONLINE\n"),time.Now());if err!=nil{t.Fatal(err)}
 if missing.BatteryVolts!=nil||missing.NominalBatteryVolts!=nil||missing.BatteryDate!=""||missing.SelfTestResult!=""{t.Fatalf("optional fields fabricated: %+v",missing)}
}
func TestBatteryHealthRejectsInvalidVoltage(t *testing.T){
 got,err:=Parse([]byte("STATUS: ONLINE\nBATTV: NaN\nNOMBATTV: -1\n"),time.Now());if err!=nil||got.BatteryVolts!=nil||got.NominalBatteryVolts!=nil{t.Fatalf("invalid reading not rejected: %+v %v",got,err)}
}
