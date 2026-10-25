import { dashboardModules } from "./dashboard-modules.mjs";
import assert from "node:assert/strict";

const counts = Object.fromEntries(["running", "current", "recent", "all", "archived"].map(scope => [scope, {families:201,sessions:403}]));
const row = id => ({id,display:{kind:"waiting",label:`Sent ${id}`,detail:"Server detail"}});
const family = id => ({id,rootId:id,projectId:"p",members:[{id},{id:`${id}-child`}]});
const page = (families, sessions, extra={}) => ({view:{catalogHostId:"host",revision:"revision",refreshAt:null,scope:"all",filters:{},counts,summary:{queuedMessages:7},surfaces:[],projects:[{id:"p",collapsedFamilyIds:["200"]}],families},presentationPosition:{hostId:"host",epoch:"epoch",sequence:2},hostEpoch:"catalog",catalogSeq:10,sessions,...extra});
const calls = [];
let restart = true;
const context = {URLSearchParams, fetch:async url => {
 const query = new URL(url,"http://test").searchParams;
 calls.push(query);
 if (query.has("cursor") && restart) {restart=false;return {ok:false,status:409};}
 if (query.has("sessionId")) return {ok:true,json:async()=>page([],query.getAll("sessionId").map(row))};
 if (query.has("cursor")) return {ok:true,json:async()=>page([family("200")],[row("200")])};
 return {ok:true,json:async()=>page(Array.from({length:200},(_,i)=>family(String(i))),Array.from({length:200},(_,i)=>row(String(i))),{nextCursor:"next"})};
},responseError:async()=>Error("Changed")};
await dashboardModules(["session-view"],context);
const result = await context.readSessionView({scope:"all",q:"server query"});
assert.equal(result.view.families.length,201);
assert.equal(result.sessions.length,402);
assert.equal(calls.filter(q=>q.has("sessionId")).length,2);
assert.equal(calls.filter(q=>!q.has("cursor")&&!q.has("sessionId")).length,2);
assert(calls.every(q=>q.get("q")==="server query"));
assert.equal(context.viewSessions(result,["200"]).map(s=>s.id).join(","),"200,200-child");
assert.throws(()=>context.requireSessionView({sessions:[]}),/position/);
assert.throws(()=>context.requireSessionView(page([],[{id:"bad",status:"busy"}])) ,/did not send/);

const selection = {app:{selected:{id:"A"},selectedPosition:{hostId:"h",epoch:"e",sequence:12}}};
await dashboardModules(["session-detail", "presentation-order"],selection);
assert(selection.acceptSelectedPresentation({session:row("B"),presentationPosition:{hostId:"h",epoch:"e",sequence:11}}));
assert.equal(selection.app.selected.id,"B","a newer background read of A cannot cancel selection of B");
assert(!selection.acceptSelectedPresentation({session:row("B"),presentationPosition:{hostId:"h",epoch:"e",sequence:10}}));
selection.rememberPresentation({hostId:"h",epoch:"new",sequence:1});
assert(!selection.acceptSelectedPresentation({session:row("B"),presentationPosition:{hostId:"h",epoch:"e",sequence:100}}),"a retired daemon epoch cannot overwrite the new one");

const requests = [];
const metadata = {daemon:{running:true},surfaces:[],queue:[],channels:[],relays:[],history:[],attention:[],deliveryProblems:[],projects:[]};
const offline = row("remote");offline.display={kind:"unavailable",label:"Unavailable",detail:"Host unreachable"};
const loader={URLSearchParams,app:{},readSessionDetail:async()=>{throw Error("Peer unreachable");},readSessionView:async params=>{requests.push(params);return page([family("remote")],[offline]);},fetch:async()=>({ok:true,json:async()=>metadata})};
await dashboardModules(["dashboard-view-loader"],loader);
const loaded = await loader.readDashboardViews(new URLSearchParams({scope:"all",q:"filtered"}),"remote");
assert.equal(loaded.detail.session.display.label,"Unavailable","a failed transcript read retains canonical selected presentation");
assert(loaded.detail.error);
assert.equal(requests[1].scope,"current");
assert.equal(requests[1].q,undefined,"overview counts are unfiltered");

const listeners = new Map();let invalidations=0;
const events={app:{},load:()=>invalidations++,setTimeout:callback=>{callback();return 1;},EventSource:class {addEventListener(name,callback){listeners.set(name,callback);} close(){} }};
await dashboardModules(["catalog-updates"],events);
events.followCatalog({hostEpoch:"epoch",catalogSeq:10});
listeners.get("session.queue")({data:JSON.stringify({seq:11})});
assert.equal(invalidations,1);
assert.equal(events.app.seenCatalog.sequence,11);
console.log("dashboard canonical paging, restart, selection, outage and invalidation passed");
