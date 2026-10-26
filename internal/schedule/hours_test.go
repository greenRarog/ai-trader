package schedule

import (
 "testing"
 "time"
 "ai-trader/internal/tinvest"
)

func TestWorkingHoursAndFixedWindows(t *testing.T){
 h,err:=ParseHours("09:30","15:00","09:30,14:00");if err!=nil{t.Fatal(err)}
 moscow:=time.FixedZone("Europe/Moscow",3*3600)
 at:=func(hour,minute,second int)time.Time{return time.Date(2026,10,27,hour,minute,second,0,moscow)}
 calendar:=tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{{Trading:true,Start:at(9,10,0),End:at(18,54,59)}}}}}
 for _,tt:=range []struct{now time.Time;allowed bool;key string}{
  {at(9,29,59),false,""},
  {at(9,30,0),true,"2026-10-27:local_0930"},
  {at(9,59,59),true,"2026-10-27:local_0930"},
  {at(10,0,0),true,""},
  {at(14,0,0),true,"2026-10-27:local_1400"},
  {at(14,29,59),true,"2026-10-27:local_1400"},
  {at(14,30,0),true,""},
  {at(14,59,59),true,""},
  {at(15,0,0),false,""},
  {at(21,0,0),false,""},
 }{
  if h.Allows(tt.now.UTC())!=tt.allowed{t.Fatalf("wrong working-hours gate at %s",tt.now)}
  key,_:=DueWithin(tt.now.UTC(),calendar,h);if key!=tt.key{t.Fatalf("%s: got %s want %s",tt.now,key,tt.key)}
 }
 calendar.Exchanges[0].Days[0].Trading=false
 if key,_:=DueWithin(at(9,30,0),calendar,h);key!=""{t.Fatal("working hours bypassed exchange closure")}
 calendar.Exchanges[0].Days[0].Trading=true
 calendar.Exchanges[0].Days[0].ClearingStart=at(14,0,0);calendar.Exchanges[0].Days[0].ClearingEnd=at(14,30,0)
 if key,_:=DueWithin(at(14,5,0),calendar,h);key!=""{t.Fatal("working hours bypassed clearing")}
}

func TestWorkingHoursValidation(t *testing.T){
 h,err:=ParseHours("","","");if err!=nil||h.Enabled||!h.Allows(time.Now()){t.Fatal("default schedule changed")}
 for _,values:=range [][3]string{
  {"9:30","15:00","09:30,14:00"},
  {"09:30","09:00","09:30,14:00"},
  {"09:30","15:00","09:00,14:00"},
  {"09:30","15:00","09:30,14:45"},
  {"09:30","15:00","09:30,09:45"},
  {"09:30","15:00","09:30"},
  {"09:30","15:00",""},
 }{if _,err:=ParseHours(values[0],values[1],values[2]);err==nil{t.Fatalf("accepted invalid hours %v",values)}}
}
