// Copyright (c) 2026 Neomantra Corp. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0-only WITH LicenseRef-NimbleTerminal-Linking-Exception OR LicenseRef-Neomantra-Commercial
// Regenerate from the two pinned upstream archive directories; no network access.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import nunjucks from 'nunjucks';
import yaml from 'js-yaml';
const [gov, apg] = process.argv.slice(2);
if (!gov || !apg) throw Error('usage: node generate.mjs GOVUK_DIR APG_DIR');
const out = path.resolve(import.meta.dirname, '../corpus');
fs.mkdirSync(out, {recursive:true});
const hashes = {}, cases = [];
const sha = b => crypto.createHash('sha256').update(b).digest('hex');
function save(name, data) { fs.mkdirSync(path.dirname(path.join(out,name)),{recursive:true}); fs.writeFileSync(path.join(out,name),data); hashes[name]=sha(data); return name; }
function copy(root, name, prefix) { return save(prefix+'/'+name, fs.readFileSync(path.join(root,name))); }
const css=copy(gov,'dist/govuk-frontend-6.6.0.min.css','govuk');
const js=copy(gov,'dist/govuk-frontend-6.6.0.min.js','govuk');
copy(apg,'content/shared/css/github.css','apg');
copy(gov,'LICENSE.txt','govuk'); copy(apg,'LICENSE.md','apg');
const env = new nunjucks.Environment(new nunjucks.FileSystemLoader(path.join(gov,'packages/govuk-frontend/src/govuk')), {autoescape:true});
const selection = [
['details','default'],['accordion','default'],['checkboxes','with conditional items'],['radios','with conditional items'],['input','with error and hint'],['textarea','with hint'],['select','with hint text and error message'],['date-input','with error message and hint'],['error-summary','default'],['tabs','default'],['service-navigation','with service name and navigation'],['pagination','default'],['summary-list','with actions'],['table','with head and caption'],['task-list','default'],['notification-banner','with type as success'],['skip-link','default'],['file-upload','default'],['password-input','default'],['character-count','default']];
for (const [component, example] of selection) {
 const source=`packages/govuk-frontend/src/govuk/components/${component}/${component}.yaml`;
 const data=yaml.load(fs.readFileSync(path.join(gov,source),'utf8'));
 const entry=data.examples.find(e=>e.name===example);
 if(!entry) throw Error(`${component}: missing ${example}`);
 const html=env.render(`components/${component}/template.njk`,{params:entry.options});
 const id='govuk-'+component;
 cases.push({id,source,example,html:save(id+'.html',html),css:[css],scripts:[{code:"document.body.className += ' js-enabled' + ('noModule' in HTMLScriptElement.prototype ? ' govuk-frontend-supported' : '');",type:''},{path:js,type:'module'},{code:"import { initAll } from './govuk/dist/govuk-frontend-6.6.0.min.js'; initAll();",type:'module'}]});
}
for(const name of ['accordion/accordion','disclosure/disclosure-navigation','tabs/tabs-manual','checkbox/checkbox','radio/radio','button/button','menu-button/menu-button-links','listbox/listbox-scrollable','combobox/combobox-select-only','dialog-modal/dialog']) {
 const [pattern,example]=name.split('/');
 const source=`content/patterns/${pattern}/examples/${example}.html`;
 const full=fs.readFileSync(path.join(apg,source),'utf8');
 const match=full.match(/<div[^>]*id="ex_start_sep"[^>]*><\/div>([\s\S]*?)<div[^>]*id="ex_end_sep"/);
 if(!match) throw Error(`missing delimiters ${source}`);
 const resolve = rel => copy(apg,path.normalize(path.join(path.dirname(source),rel)),'apg');
 const styles=[...full.matchAll(/<link\b[^>]*>/g)].map(m=>m[0]).filter(s=>/stylesheet/.test(s)).map(s=>s.match(/href="([^"]+)"/)[1]).filter(s=>!s.startsWith('http')).map(resolve);
 const scripts=[...full.matchAll(/<script\b[^>]*src="([^"]+)"[^>]*><\/script>/g)].map(m=>m[1]).filter(s=> !/\/(examples|highlight.pack|app|skipto)\.js$/.test(s)).map(s=>({path:resolve(s),type:''}));
 const id='apg-'+example;
 cases.push({id,source,html:save(id+'.html',match[1]),css:styles,scripts});
}
fs.writeFileSync(path.join(out,'manifest.json'),JSON.stringify({upstream:{govuk:'a11bd3316363ab4702fcef81d6ce06a947c464ef',apg:'f14b2490a6728cda85fd7feafbd148b3c3eaf038'},cases,sha256:hashes},null,2)+'\n');
