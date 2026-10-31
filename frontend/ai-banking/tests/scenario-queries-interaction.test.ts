import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import * as Vue from 'vue'
import {createRenderer,defineComponent,h,nextTick,reactive,ssrContextKey} from 'vue'
import {readFileSync} from 'node:fs'
import {parse,compileScript,compileTemplate} from '@vue/compiler-sfc'
import {transformSync} from 'esbuild'
import ScenarioQueries from '../src/components/assistant/ScenarioQueries.vue'

const io=vi.hoisted(()=>({session:null as any,submit:vi.fn(),run:vi.fn()}))
vi.mock('../src/lib/store',()=>({useSession:()=>io.session}))
vi.mock('../src/api/agent-client',()=>({agentApi:{submit:io.submit,run:io.run}}))
vi.mock('../src/components/ui/button/Button.vue',()=>({default:defineComponent({setup(_,{attrs,slots}){return()=>h('button',attrs,slots.default?.())}})}))

// Execute the actual compiled template using Vue's renderer; no DOM/network dependency.
const {descriptor}=parse(readFileSync(new URL('../src/components/assistant/ScenarioQueries.vue',import.meta.url),'utf8'))
const bindings=compileScript(descriptor,{id:'scenario-queries'}).bindings
const template=compileTemplate({source:descriptor.template!.content,filename:'ScenarioQueries.vue',id:'scenario-queries',compilerOptions:{bindingMetadata:bindings}})
const code=transformSync(template.code,{loader:'ts'}).code.replace(/import \{([^}]+)\} from "vue"/g,(_,names:string)=>'const {'+names.replace(/ as /g,':')+'} = Vue').replace('export function render','return function render')
;(ScenarioQueries as any).render=new Function('Vue',code)(Vue)
class Element {
 children:Element[]=[];parent:Element|null=null;props:Record<string,any>={};text='';value=''
 constructor(public tag:string){}
 addEventListener(){} removeEventListener(){}
}
const renderer=createRenderer<Element,Element>({
 createElement:tag=>new Element(tag),createText:text=>Object.assign(new Element('#text'),{text}),createComment:()=>new Element('#comment'),
 setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},
 parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,
 insert(node,parent,anchor){node.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(node);else parent.children.splice(index,0,node)},
 remove(node){node.parent?.children.splice(node.parent.children.indexOf(node),1);node.parent=null},patchProp(node,key,_old,value){node.props[key]=value},
})
const all=(node:Element):Element[]=>[node,...node.children.flatMap(all)]
const text=(node:Element):string=>node.text+node.children.map(text).join('')
const cleanups:(()=>void)[]=[]
const task={task_id:'lg-11111111-1111-4111-8111-111111111111',status:'queued',version:1}
async function mount(){
 const selected=vi.fn(),root=new Element('root'),app=renderer.createApp(ScenarioQueries,{onSelected:selected})
 app.provide(ssrContextKey,{modules:new Set()});app.mount(root);cleanups.push(()=>app.unmount());await nextTick()
 return {root,selected,click:(name:string)=>{const button=all(root).find(node=>node.tag==='button'&&text(node)===name);expect(button).toBeDefined();button!.props.onClick()}}
}
beforeEach(()=>{
 io.session=reactive({sessionEpoch:1,user:{id:'7'},accountId:'23',connected:true})
 io.submit.mockReset();io.run.mockReset()
 vi.stubGlobal('fetch',vi.fn(()=>{throw new Error('Network prohibited in isolated interaction test')}))
})
afterEach(()=>{cleanups.splice(0).forEach(cleanup=>cleanup());vi.unstubAllGlobals()})

it('ignores rapid duplicate clicks and runs only the explicit read-only task once',async()=>{
 let finish!:(value:any)=>void
 io.submit.mockImplementation(()=>new Promise(resolve=>{finish=resolve}));io.run.mockResolvedValue({...task,status:'succeeded'})
 const view=await mount();view.click('查询卡片');view.click('查询卡片')
 expect(io.submit).toHaveBeenCalledOnce();expect(io.submit.mock.calls[0]![0].tasks[0]).toMatchObject({tool:'get_scenario_overview',use_coze:false,execution_mode:'read_only'})
 finish(task);await vi.waitFor(()=>expect(io.run).toHaveBeenCalledExactlyOnceWith(task.task_id,1))
 expect(view.selected).toHaveBeenCalledExactlyOnceWith(task.task_id)
 expect(globalThis.fetch).not.toHaveBeenCalled()
})
it('retains the transport key after uncertain submission and reuses it for the same query',async()=>{
 io.submit.mockRejectedValueOnce(new Error('network unavailable')).mockResolvedValueOnce(task)
 io.run.mockResolvedValue({...task,status:'succeeded'})
 const view=await mount();view.click('查询订阅')
 await vi.waitFor(()=>expect(text(view.root)).toContain('提交结果不确定'))
 const first=io.submit.mock.calls[0]![1];view.click('查询订阅')
 await vi.waitFor(()=>expect(io.run).toHaveBeenCalledOnce())
 expect(io.submit.mock.calls[1]![1]).toBe(first)
 expect(io.submit.mock.calls[1]![0]).toEqual(io.submit.mock.calls[0]![0])
})
it('retains a known task after run timeout and viewing it does not replay the run',async()=>{
 io.submit.mockResolvedValue(task);io.run.mockRejectedValue(new Error('timeout'))
 const view=await mount();view.click('查询账单')
 await vi.waitFor(()=>expect(text(view.root)).toContain('任务已保存'))
 view.click('查看已保存的查询任务 →');await nextTick()
 expect(io.submit).toHaveBeenCalledOnce();expect(io.run).toHaveBeenCalledOnce()
 expect(view.selected).toHaveBeenNthCalledWith(2,task.task_id)
})
it('does not run or show the old task when the selected account changes while submitting',async()=>{
 let finish!:(value:any)=>void
 io.submit.mockImplementation(()=>new Promise(resolve=>{finish=resolve}))
 const view=await mount();view.click('查询生日安排')
 io.session.accountId='24';await nextTick();finish(task);await nextTick();await nextTick()
 expect(io.run).not.toHaveBeenCalled();expect(view.selected).not.toHaveBeenCalled()
 expect(text(view.root)).not.toContain('查看已保存的查询任务')
})
