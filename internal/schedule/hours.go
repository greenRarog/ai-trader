package schedule

import (
 "fmt"
 "strings"
 "time"

 "ai-trader/internal/tinvest"
)

// Hours restricts automatic broker/model requests to a Moscow daytime window.
// Its zero value keeps the exchange-derived schedule.
type Hours struct {
 Enabled bool
 Start,End int // minutes after Moscow midnight
 Analyses []int
}

func minute(value string)(int,error){
 parsed,err:=time.Parse("15:04",value)
 if err!=nil||len(value)!=5{return 0,fmt.Errorf("time must use HH:MM format")}
 return parsed.Hour()*60+parsed.Minute(),nil
}

func ParseHours(start,end,analyses string)(Hours,error){
 var h Hours
 if start==""&&end==""&&analyses==""{return h,nil}
 var err error
 h.Start,err=minute(start);if err!=nil{return h,fmt.Errorf("AUTOMATION_START_MSK: %w",err)}
 h.End,err=minute(end);if err!=nil||h.End<=h.Start{return h,fmt.Errorf("AUTOMATION_END_MSK must be later than start on the same day")}
 for _,text:=range strings.Split(analyses,","){
  at,err:=minute(strings.TrimSpace(text));if err!=nil{return h,fmt.Errorf("AUTOMATION_ANALYSIS_TIMES_MSK: %w",err)}
  if at<h.Start||at+30>h.End{return h,fmt.Errorf("analysis windows must fit within automation hours")}
  if len(h.Analyses)>0&&at<h.Analyses[len(h.Analyses)-1]+30{return h,fmt.Errorf("analysis windows must be ordered and not overlap")}
  h.Analyses=append(h.Analyses,at)
 }
 if len(h.Analyses)!=2{return h,fmt.Errorf("AUTOMATION_ANALYSIS_TIMES_MSK must contain exactly two times")}
 h.Enabled=true
 return h,nil
}

func(h Hours)Bounds(now time.Time)(time.Time,time.Time){
 local:=now.In(time.FixedZone("Europe/Moscow",3*3600))
 midnight:=time.Date(local.Year(),local.Month(),local.Day(),0,0,0,0,local.Location())
 return midnight.Add(time.Duration(h.Start)*time.Minute),midnight.Add(time.Duration(h.End)*time.Minute)
}
func(h Hours)Allows(now time.Time)bool{
 if !h.Enabled{return true}
 start,end:=h.Bounds(now);return !now.Before(start)&&now.Before(end)
}

func DueWithin(now time.Time,calendars tinvest.Schedules,h Hours)(string,string){
 if !h.Enabled{return Due(now,calendars)}
 if !h.Allows(now){return "","outside configured Moscow working hours"}
 for _,exchange:=range calendars.Exchanges{
  if exchange.Exchange!="MOEX"{continue}
  for _,day:=range exchange.Days{
   if day.Start.IsZero()||now.Before(day.Start)||!now.Before(day.End)||!day.OpenAt(now){continue}
   start,_:=h.Bounds(now)
   for _,at:=range h.Analyses{
    opens:=start.Add(time.Duration(at-h.Start)*time.Minute)
    if !now.Before(opens)&&now.Before(opens.Add(30*time.Minute)){
     return fmt.Sprintf("%s:local_%02d%02d",opens.Format("2006-01-02"),at/60,at%60),"configured Moscow analysis window"
    }
   }
  }
 }
 return "","outside configured analysis windows or exchange is closed"
}
