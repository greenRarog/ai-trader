package schedule

import (
 "testing"
 "time"
 "ai-trader/internal/tinvest"
)
func TestTwoBoundedWindows(t *testing.T){
 start:=time.Date(2026,10,26,6,10,0,0,time.UTC);end:=start.Add(9*time.Hour)
 day:=tinvest.TradingDay{Trading:true,Start:start,End:end}
 calendar:=func(d tinvest.TradingDay)tinvest.Schedules{return tinvest.Schedules{Exchanges:[]tinvest.Schedule{{Exchange:"MOEX",Days:[]tinvest.TradingDay{d}}}}}
 for _,tt:=range []struct{at time.Time;key string}{{start,""},{start.Add(time.Hour),"2026-10-26:after_open"},{start.Add(89*time.Minute),"2026-10-26:after_open"},{start.Add(90*time.Minute),""},{end.Add(-time.Hour),"2026-10-26:before_close"},{end.Add(-30*time.Minute),""},{end,""}}{
  key,_:=Due(tt.at,calendar(day));if key!=tt.key{t.Fatalf("%s: %s, want %s",tt.at,key,tt.key)}
 }
 day.Trading=false;if key,_:=Due(start.Add(time.Hour),calendar(day));key!=""{t.Fatal("called on closed day")}
 day.Trading=true;day.ClearingStart=start.Add(time.Hour);day.ClearingEnd=start.Add(2*time.Hour)
 if key,_:=Due(start.Add(time.Hour),calendar(day));key!=""{t.Fatal("called during clearing")}
 day.ClearingStart=time.Time{};day.ClearingEnd=time.Time{};day.End=start.Add(2*time.Hour)
 if key,_:=Due(start.Add(time.Hour),calendar(day));key!=""{t.Fatal("overlapping windows for short session")}
 if key,_:=Due(start.Add(time.Hour),tinvest.Schedules{});key!=""{t.Fatal("guessed absent schedule")}
}
