// Package schedule selects two bounded analysis windows from a broker calendar.
package schedule

import (
 "fmt"
 "time"
 "ai-trader/internal/tinvest"
)

// Due uses the main session only, not evening/dealer/weekend sessions. A missed
// 30-minute window is not caught up; restarting cannot cause a burst of calls.
func Due(now time.Time,calendars tinvest.Schedules)(string,string){
 for _,exchange:=range calendars.Exchanges{
  if exchange.Exchange!="MOEX"{continue}
  for _,day:=range exchange.Days{
   if !day.Trading||day.Start.IsZero()||day.End.Sub(day.Start)<3*time.Hour||!day.OpenAt(now){continue}
   for _,slot:=range []struct{name string;at time.Time}{{"after_open",day.Start.Add(time.Hour)},{"before_close",day.End.Add(-time.Hour)}}{
    if !now.Before(slot.at)&&now.Before(slot.at.Add(30*time.Minute)){
     date:=day.Start.In(time.FixedZone("Europe/Moscow",3*3600)).Format("2006-01-02")
     return fmt.Sprintf("%s:%s",date,slot.name),"scheduled analysis window"
    }
   }
  }
 }
 return "","outside the two main-session analysis windows"
}
