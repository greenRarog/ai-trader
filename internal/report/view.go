package report

import (
 "bytes"
 "encoding/json"
 "fmt"
 "strconv"
 "strings"
)
// View presents exact decimal rubles/percentages instead of internal nanos/bps.
func View(s Summary)(any,error){
 raw,err:=json.Marshal(s);if err!=nil{return nil,err};decoder:=json.NewDecoder(bytes.NewReader(raw));decoder.UseNumber();var value any;if err:=decoder.Decode(&value);err!=nil{return nil,err}
 var convert func(any)(any,error)
 convert=func(v any)(any,error){switch x:=v.(type){
 case map[string]any:
  out:=map[string]any{}
  for key,value:=range x{
   places:=0;name:=key
   if strings.HasSuffix(key,"_nano"){places=9;name=strings.TrimSuffix(key,"_nano")+"_rub"}else if strings.HasSuffix(key,"_bps"){places=2;name=strings.TrimSuffix(key,"_bps")+"_pct"}
   if places>0&&value!=nil{n,ok:=value.(json.Number);if !ok{return nil,fmt.Errorf("invalid monetary report field")};number,err:=strconv.ParseInt(n.String(),10,64);if err!=nil{return nil,err};out[name]=decimal(number,places)}else{converted,err:=convert(value);if err!=nil{return nil,err};out[name]=converted}
  };return out,nil
 case []any:for i,element:=range x{converted,err:=convert(element);if err!=nil{return nil,err};x[i]=converted};return x,nil
 default:return v,nil
 }}
 converted,err:=convert(value);if err!=nil{return nil,err};converted.(map[string]any)["money_unit"]="RUB and percentages as exact decimal strings";return converted,nil
}
func decimal(n int64,places int)string{
 text:=strconv.FormatInt(n,10);negative:=strings.HasPrefix(text,"-");text=strings.TrimPrefix(text,"-");for len(text)<=places{text="0"+text};at:=len(text)-places;fraction:=strings.TrimRight(text[at:],"0");text=text[:at];if fraction!=""{text+="."+fraction};if negative{text="-"+text};return text
}
