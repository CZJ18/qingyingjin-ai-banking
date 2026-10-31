export type BillMode='month'|'year'
export function billDates(mode:BillMode,month:string,year:number){
 if(mode==='year')return {start:`${year}-01-01`,end:`${year}-12-31`}
 if(!/^\d{4}-(0[1-9]|1[0-2])$/.test(month))return {start:'',end:''}
 const [y,m]=month.split('-').map(Number)
 return {start:month+'-01',end:month+'-'+String(new Date(Date.UTC(y!,m!,0)).getUTCDate()).padStart(2,'0')}
}
export function validBillRange(mode:BillMode,month:string,year:number,start:string,end:string){
 const validDate=(s:string)=>/^\d{4}-\d{2}-\d{2}$/.test(s)&&Number.isFinite(Date.parse(s+'T00:00:00Z'))&&new Date(s+'T00:00:00Z').toISOString().slice(0,10)===s
 if(!validDate(start)||!validDate(end)||start>end)return false
 const bounds=billDates(mode,month,year)
 return start>=bounds.start&&end<=bounds.end&&bounds.start!==''
}
