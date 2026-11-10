import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
function load(source,imports={},globals={}) {
  const module={exports:{}};
  const code=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
  new Function("require","module","exports",...Object.keys(globals),code)(name=>imports[name],module,module.exports,...Object.values(globals));
  return module.exports;
}
const model=load(await readFile(new URL("../src/gitManagement.ts",import.meta.url),"utf8"));
const pagination=load(await readFile(new URL("../src/pagination.ts",import.meta.url),"utf8"),{vue});
const source=(await readFile(new URL("../src/GitManagementPanel.vue",import.meta.url),"utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
const profile={...model.newGitProfile(),id:"profile",hostId:"host",systemAccount:"flycode",name:"工作账号",sshHost:"git.example.test",username:"developer",credentialId:"saved-token",credentialRevision:"1",version:"1",configuredVersion:"1"};
test("a configured Git account can be reused for GitHub and self-hosted GitLab URLs", () => {
  assert.equal(model.gitAccountRemote({...profile,sshHost:"github.com"}, "git@github.com:owner/project.git"), "https://developer@github.com/owner/project.git");
  assert.equal(model.gitAccountRemote({...profile,authMethod:"SSH",sshPort:2222}, "ssh://git@git.example.test:2222/team/subgroup/project.git"), "git@ops-git-profile:team/subgroup/project.git");
  assert.throws(() => model.gitAccountRemote(profile, "https://elsewhere.test/group/repo.git"));
  assert.throws(() => model.gitAccountRemote(profile, "https://developer:secret@git.example.test/group/repo.git"));
});
class ApiError extends Error { constructor(code) {super(code);this.code=code;} }
async function flush(){ for(let i=0;i<12;i++){await Promise.resolve();await vue.nextTick();} }
function harness(custom) {
  const calls=[],unmount=[],props=vue.reactive({host:{id:"host",name:"服务器",kind:"SSH",sshUser:"flycode",version:"1"},active:true});
  const request=async(path,method="GET",body,signal)=>{
    calls.push({path,method,body:body&&structuredClone(body),signal});
    const override=custom&&await custom(path,method,body);if(override!==undefined)return override;
    if(path==="/git-accounts"&&method==="GET")return [profile];
    if(path==="/credentials")return {id:"token",revision:"1"};
    if(path.endsWith("/git-keys"))return body.operation==="HOST_KEY"?{hostPublicKey:"ssh-ed25519 AAAAhost",hostFingerprint:"SHA256:host"}:{id:body.id,account:body.account,accountUid:"1000",keyPath:`/home/${body.account}/.local/share/streamfusion-ops/git-keys/${body.id}/key`,publicKey:"ssh-ed25519 AAAAaccount",fingerprint:"SHA256:account"};
    if(path.startsWith("/git-accounts"))return {};
    if(path==="/hosts")return [{...props.host,version:"2",capabilities:{mutation:{status:"AVAILABLE"}}}];
    throw Error("Unexpected "+path);
  };
  const scope=vue.effectScope();
  const flow=scope.run(()=>load(source+"\nexport const flow={profiles,profileDraft,token,generatedKey,error,busy,preparationOpen,editProfile,generateTargetKey,scanHostKey,resetTargetKey,saveProfile,configureProfile,removeProfile,clearDraft,changeMethod,preparationUpdated,page,pageSize,pagedProfiles,usageProfile,repository};",{
    vue:{...vue,onUnmounted:fn=>unmount.push(fn)},"./api":{request,ApiError},"./gitManagement":model,"./pagination":pagination,
  },{defineProps:()=>props,withDefaults:x=>x,defineEmits:()=>()=>{}}).flow);
  return {flow,calls,props,close(){unmount.forEach(fn=>fn());scope.stop();}};
}
test("Git defaults to HTTPS and saves token through the credential API without including its value in account metadata",async()=>{
  const h=harness();await flush();h.flow.editProfile();
  assert.equal(h.flow.profileDraft.value.authMethod,"HTTPS");
  Object.assign(h.flow.profileDraft.value,{name:"公司",sshHost:"github.com",username:"flycodeu"});h.flow.token.value="fixture-token";
  await h.flow.saveProfile();
  const secret=h.calls.find(c=>c.path==="/credentials"),saved=h.calls.find(c=>c.path==="/git-accounts"&&c.method==="POST");
  assert.equal(secret.body.kind,"TOKEN");assert.equal(saved.body.credentialId,"token");
  assert.equal(JSON.stringify(saved.body).includes("fixture-token"),false);
  assert.equal(h.flow.token.value,"");assert.equal(saved.body.authorEmail,"");assert.equal(saved.body.hostPublicKey,"");
  assert.equal(h.calls.some(c=>c.path.includes("repository")),false);h.close();
});

test("Git pagination preserves an account usage draft and clamps after deletion",async()=>{
  const h=harness();await flush();
  h.flow.profiles.value=Array.from({length:21},(_,index)=>({...profile,id:`profile-${index}`}));
  h.flow.usageProfile.value="profile-0";h.flow.repository.value="https://git.example.test/team/project.git";
  h.flow.page.value=3;
  assert.equal(h.flow.pagedProfiles.value.length,1);
  h.flow.profiles.value=h.flow.profiles.value.slice(0,20);
  assert.equal(h.flow.page.value,2);
  assert.equal(h.flow.pagedProfiles.value.length,10);
  assert.equal(h.flow.usageProfile.value,"profile-0");
  assert.equal(h.flow.repository.value,"https://git.example.test/team/project.git");
  h.flow.pageSize.value=20;assert.equal(h.flow.page.value,1);h.close();
});
test("SSH key retry reuses the target key, saved identity edits reuse its public key, and host trust stays explicit",async()=>{
  let count=0;
  const h=harness((path,method,body)=>{if(path.endsWith("/git-keys")&&body.operation==="GENERATE"&&++count===1)throw Error("lost response");});
  await flush();h.flow.editProfile();h.flow.profileDraft.value.authMethod="SSH";h.flow.changeMethod();
  Object.assign(h.flow.profileDraft.value,{name:"自建GitLab",sshHost:"git.example.test"});
  await h.flow.generateTargetKey();await h.flow.generateTargetKey();
  const attempts=h.calls.filter(c=>c.path.endsWith("/git-keys"));assert.equal(attempts[0].body.id,attempts[1].body.id);
  await h.flow.scanHostKey();await flush();assert.equal(h.flow.profileDraft.value.hostKeyConfirmed,false);
  await h.flow.saveProfile();assert.equal(h.calls.some(c=>c.path==="/git-accounts"&&c.method==="POST"),false);
  h.flow.profileDraft.value.hostKeyConfirmed=true;await h.flow.saveProfile();
  const saved=h.calls.find(c=>c.path==="/git-accounts"&&c.method==="POST").body;
  h.flow.editProfile({...profile,...saved,id:"ssh-profile",version:"1",keyFingerprint:"SHA256:account"});
  assert.equal(h.flow.generatedKey.value.publicKey,"ssh-ed25519 AAAAaccount");
  h.flow.resetTargetKey();assert.equal(h.flow.generatedKey.value,undefined);h.close();
});
test("same host refresh retains focus state and token is erased when the panel closes",async()=>{
  const h=harness();await flush();h.flow.editProfile();h.flow.token.value="draft-secret";h.flow.profileDraft.value.name="正在填写";
  const calls=h.calls.length;h.props.host={...h.props.host,version:"2"};await flush();
  assert.equal(h.calls.length,calls);assert.equal(h.flow.profileDraft.value.name,"正在填写");
  h.props.active=false;await flush();assert.equal(h.flow.token.value,"");h.close();
});
test("account configuration is explicit, deletion removes its target configuration first",async()=>{
  const h=harness();await flush();assert.equal(h.calls.some(c=>c.path.endsWith("/configure")),false);
  await h.flow.configureProfile(profile);
  assert.deepEqual(h.calls.find(c=>c.path.endsWith("/configure")).body,{expectedVersion:"1",expectedHostVersion:"1"});
  await h.flow.removeProfile(profile);
  assert.deepEqual(h.calls.filter(c=>c.method!=="GET").map(c=>c.path),["/git-accounts/profile/configure","/git-accounts/profile/remove-configuration","/git-accounts/profile"]);
  h.close();
});
test("late target key response cannot populate another host form and empty host-key replies show an actionable error",async()=>{
  let finish;
  const h=harness((path,method,body)=>{if(path.endsWith("/git-keys")&&body.operation==="GENERATE")return new Promise(resolve=>finish=resolve);if(body?.operation==="HOST_KEY")return {};});
  await flush();h.flow.editProfile();h.flow.profileDraft.value.authMethod="SSH";h.flow.changeMethod();h.flow.profileDraft.value.sshHost="github.com";
  await h.flow.scanHostKey();assert.match(h.flow.error.value.message,/没有读取到/);
  const loading=h.flow.generateTargetKey();await flush();h.props.host={...h.props.host,id:"other"};await flush();
  finish({publicKey:"late",keyPath:"/late",fingerprint:"late"});await loading;
  assert.equal(h.flow.generatedKey.value,undefined);assert.equal(h.flow.profileDraft.value,undefined);h.close();
});
