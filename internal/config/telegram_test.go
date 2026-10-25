package config

import "testing"
func TestTelegramIsOptionalAndValidatesSecretsLocally(t *testing.T){
 t.Setenv("TELEGRAM_ENABLED","false");t.Setenv("TELEGRAM_BOT_TOKEN","");t.Setenv("TELEGRAM_CHAT_ID","")
 if c,err:=LoadTelegram();err!=nil||c.Enabled{t.Fatal(err)}
 t.Setenv("TELEGRAM_ENABLED","true");if _,err:=LoadTelegram();err==nil{t.Fatal("accepted enabled Telegram without settings")}
 t.Setenv("TELEGRAM_BOT_TOKEN","123:fake");t.Setenv("TELEGRAM_CHAT_ID","42");if _,err:=LoadTelegram();err!=nil{t.Fatal(err)}
 t.Setenv("TELEGRAM_BOT_TOKEN","123:fake/../../");if _,err:=LoadTelegram();err==nil{t.Fatal("accepted token path injection")}
}
