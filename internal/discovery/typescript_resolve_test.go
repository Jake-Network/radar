package discovery

import (
	"context"
	"strings"
	"testing"
)

func TestResolveConsumersAliasesBarrelsAxiosAndFetch(t *testing.T) {
	sources := map[string]string{
		"web/types.ts":  `export interface User { name: string; profile: { city: string } }`,
		"web/barrel.ts": `export type { User as Response } from './types';`,
		"web/index.ts":  `export * from './barrel';`,
		"web/client.ts": `import type { Response as Person } from './index';
 import request from 'axios';
 async function fetchUser() {
 const user: Person = await (await fetch('/users')).json();
 console.log(user.name, user.profile.city);
 function other(user: Person) { console.log(user.unrelated); }
 }
 async function axiosUser() {
 const result = await request.get<Person>('/users');
 console.log(result.data.name, result.status);
 }
 const api = request.create();
 async function instanceUser() {
 const result = await api.post<Person>('/users');
 console.log(result.data.profile.city);
 }`,
	}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) > 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
	if len(responses) != 3 {
		t.Fatalf("responses=%+v", responses)
	}
	for _, r := range responses {
		if r.typePath != "web/types.ts" || r.typeName != "User" || r.endpoint != "/users" {
			t.Fatalf("identity=%+v", r)
		}
		for _, f := range r.fields {
			if strings.Contains(f, "unrelated") || f == "status" {
				t.Fatalf("unrelated field: %+v", r)
			}
		}
	}
	if strings.Join(responses[0].fields, ",") != "name,profile.city" {
		t.Fatalf("fetch fields=%+v", responses[0])
	}
	if responses[1].method != "GET" || strings.Join(responses[1].fields, ",") != "name" || len(responses[1].ambiguities) == 0 {
		t.Fatalf("axios=%+v", responses[1])
	}
	if responses[2].method != "POST" || strings.Join(responses[2].fields, ",") != "profile.city" {
		t.Fatalf("instance=%+v", responses[2])
	}
}
func TestResolveConsumersUnresolvedAndUnrelated(t *testing.T) {
	sources := map[string]string{
		"a.ts":      `export interface Response { one: string }`,
		"b.ts":      `export interface Response { two: string }`,
		"barrel.ts": `export * from './a'; export * from './b';`,
		"client.ts": `import type { Response } from './barrel'; import axios from 'axios';
 async function run() { const result = await axios.get<Response>('/users'); console.log(result.data.one); }`,
		"dynamic.ts": `import type { Response } from './a'; import axios from 'axios';
 async function run(url: string) { const result = await axios.get<Response>(url); console.log(result.data.one); }`,
		"unrelated.ts": `import type { Response } from './b';
 const axios = { get: async <T>(url: string) => ({} as T) };
 async function run() { const result = await axios.get<Response>('/users'); console.log(result.two); }`,
	}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || len(diagnostics) != 2 {
		t.Fatalf("responses=%+v diagnostics=%+v", responses, diagnostics)
	}
}
func TestResolveConsumersCyclesAndScope(t *testing.T) {
	sources := map[string]string{
		"a.ts": `export * from './b';`, "b.ts": `export * from './a';`,
		"types.ts": `export type Result = { ok: boolean };`,
		"client.ts": `import axios from 'axios'; import type { Result } from './types';
 async function run() { const result = await axios.get<Result>('/users');
 { const result = { data: { wrong: true } }; console.log(result.data.wrong); }
 console.log(result.data.ok);
 const callback = (result: any) => console.log(result.data.shadow);
 }
 function fake(axios: any) { const result = axios.get<Result>('/bad'); console.log(result.data.wrong); }
 `,
		"cycle.ts": `import type { Result } from './a'; import axios from 'axios';
 async function run() { const result = await axios.get<Result>('/cycle'); console.log(result.data.ok); }`,
	}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || strings.Join(responses[0].fields, ",") != "ok" || len(diagnostics) != 1 {
		t.Fatalf("responses=%+v diagnostics=%+v", responses, diagnostics)
	}
}

func TestResolveConsumersRejectsMutableAndShadowedRelationships(t *testing.T) {
	sources := map[string]string{
		"types.ts": `export interface Response { email: string }`,
		"mutable.ts": `import axios from 'axios'; import type {Response} from './types';
 async function f(){ let r=await axios.get<Response>('/users'); r={data:{other:true}}; console.log(r.data.email); }`,
		"shadowtype.ts": `import type {Response} from './types';
 async function f(){ type Response={other:boolean}; const r:Response=await (await fetch('/users')).json(); console.log(r.email); }`,
		"shadowfetch.ts": `import type {Response} from './types';
 function fetch(url:string){return {json:()=>({email:'x'})}};
 async function f(){const r:Response=await (await fetch('/users')).json(); console.log(r.email); }`,
		"importfetch.ts": `import type {Response} from './types'; import fetch from './fake';
 async function f(){const r:Response=await (await fetch('/users')).json(); console.log(r.email); }`,
		"shadowaxios.ts": `import axios from 'axios'; import type {Response} from './types';
 function f(){ function axios(){};const r=axios.get<Response>('/users');console.log(r.data.email); }`,
	}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || len(diagnostics) != 2 {
		t.Fatalf("responses=%+v diagnostics=%+v", responses, diagnostics)
	}
}

func TestResolveConsumersSplitFetchResponse(t *testing.T) {
	sources := map[string]string{"types.ts": `export interface Response { email:string }`,
		"client.ts": `import type {Response} from './types';
 async function f(){const http=await fetch('/users',{method:'POST'}); const body=await http.json() as Response;console.log(body.email);}
 async function dynamic(url:string){const http=await fetch(url);const body:Response=await http.json();console.log(body.email);}
 async function mutated(){let http=await fetch('/users');http=other;const body:Response=await http.json();console.log(body.email);}`}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 1 || responses[0].method != "POST" || strings.Join(responses[0].fields, ",") != "email" || len(diagnostics) != 1 {
		t.Fatalf("responses=%+v diagnostics=%+v", responses, diagnostics)
	}
}

func TestResolveConsumersRejectsAxiosBaseURLAndPropertyMutations(t *testing.T) {
	sources := map[string]string{"types.ts": `export interface R { name:string }`,
		"client.ts": `import axios from 'axios';import type {R} from './types';
 const prefix=axios.create({baseURL:'/api'});
 const dynamic=axios.create({baseURL:runtime});
 const spread=axios.create({...config});
 async function f(){
 const a=await prefix.get<R>('/users');console.log(a.data.name);
 const b=await dynamic.get<R>('/users');console.log(b.data.name);
 const c=await spread.get<R>('/users');console.log(c.data.name);
 const d=await axios.get<R>('/users',{baseURL:'/api'});console.log(d.data.name);
 const r=await axios.get<R>('/users');r.data={name:'local'};console.log(r.data.name);
 const s=await axios.get<R>('/users');s['data']={name:'local'};console.log(s.data.name);
 }`}
	responses, diagnostics, err := resolveConsumers(context.Background(), sources, "rev")
	if err != nil {
		t.Fatal(err)
	}
	if len(responses) != 0 || len(diagnostics) != 6 {
		t.Fatalf("responses=%+v diagnostics=%+v", responses, diagnostics)
	}
}
