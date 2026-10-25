package config

import (
 "fmt"
 "os"
 "strconv"
 "strings"
 "time"
)
type Telegram struct{Enabled bool;Token,ChatID string;Timeout time.Duration}
func LoadTelegram()(Telegram,error){
 c:=Telegram{Timeout:10*time.Second};var err error
 c.Enabled,err=strconv.ParseBool(value("TELEGRAM_ENABLED","false"));if err!=nil{return c,fmt.Errorf("TELEGRAM_ENABLED must be true or false")};if !c.Enabled{return c,nil}
 c.Token=strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"));c.ChatID=strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
 if c.Token==""||strings.ContainsAny(c.Token,"/\\?&# \t\r\n")||!strings.Contains(c.Token,":"){return c,fmt.Errorf("set a valid TELEGRAM_BOT_TOKEN in local .env")}
 if c.ChatID==""||strings.ContainsAny(c.ChatID,"\r\n"){return c,fmt.Errorf("set TELEGRAM_CHAT_ID in local .env")}
 return c,nil
}
