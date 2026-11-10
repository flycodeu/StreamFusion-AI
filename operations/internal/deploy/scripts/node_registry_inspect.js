const fs=require('fs'),crypto=require('crypto');
const ini=require(process.argv[1]);const file=process.argv[2];
let raw='';try{raw=fs.readFileSync(file,'utf8');}catch(e){if(e.code!=='ENOENT')process.exit(1);}
if(Buffer.byteLength(raw)>131072)process.exit(1);
const config=ini.parse(raw);const value=config.registry;delete config.registry;
function sorted(v){if(Array.isArray(v))return v.map(sorted);if(v&&typeof v==='object')return Object.fromEntries(Object.keys(v).sort().map(k=>[k,sorted(v[k])]));return v;}
const otherHash=crypto.createHash('sha256').update(JSON.stringify(sorted(config))).digest('hex');
let registry='',configured=value!==undefined,redacted=false;
if(configured){if(typeof value!=='string'||value.length>2048){redacted=true;}else{try{const u=new URL(value);if(!['http:','https:'].includes(u.protocol)||u.username||u.password||u.search||u.hash||/[\s\x00-\x1f\x7f]/.test(value)){redacted=true;}else{registry=value;}}catch{redacted=true;}}}
process.stdout.write(JSON.stringify({registry,configured,redacted,otherHash}));