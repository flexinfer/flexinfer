#!/usr/bin/env node
"use strict";
const __loom_spawn_driver_meta_url = require('url').pathToFileURL(__filename).href;
"use strict";
var __create = Object.create;
var __defProp = Object.defineProperty;
var __getOwnPropDesc = Object.getOwnPropertyDescriptor;
var __getOwnPropNames = Object.getOwnPropertyNames;
var __getProtoOf = Object.getPrototypeOf;
var __hasOwnProp = Object.prototype.hasOwnProperty;
var __copyProps = (to, from, except, desc) => {
  if (from && typeof from === "object" || typeof from === "function") {
    for (let key of __getOwnPropNames(from))
      if (!__hasOwnProp.call(to, key) && key !== except)
        __defProp(to, key, { get: () => from[key], enumerable: !(desc = __getOwnPropDesc(from, key)) || desc.enumerable });
  }
  return to;
};
var __toESM = (mod, isNodeMode, target) => (target = mod != null ? __create(__getProtoOf(mod)) : {}, __copyProps(
  // If the importer is in node compatibility mode or this is not an ESM
  // file that has been converted to a CommonJS file using a Babel-
  // compatible transform (i.e. "__esModule" has not been set), then set
  // "default" to the CommonJS "module.exports" for node compatibility.
  isNodeMode || !mod || !mod.__esModule ? __defProp(target, "default", { value: mod, enumerable: true }) : target,
  mod
));

// src/cli.ts
var DEFAULT_ARGS = {
  agentType: "claude-code",
  task: "",
  agentId: "",
  spawnId: "",
  workingDir: "",
  maxTurns: 0,
  maxCostUsd: 0,
  controlPort: 0,
  controlFile: "",
  multiTurn: false,
  dryRun: false
};
function parseArgs(argv) {
  const args = { ...DEFAULT_ARGS };
  for (let i3 = 2; i3 < argv.length; i3++) {
    const flag = argv[i3];
    if (typeof flag !== "string" || !flag.startsWith("--")) continue;
    const key = flag.slice(2);
    const next = argv[i3 + 1];
    if (key === "dry-run") {
      args.dryRun = true;
      continue;
    }
    if (key === "multi-turn") {
      args.multiTurn = true;
      continue;
    }
    if (next === void 0 || next.startsWith("--")) continue;
    i3++;
    switch (key) {
      case "agent-type":
        if (next === "claude-code" || next === "claude" || next === "codex") {
          args.agentType = next;
        }
        break;
      case "task":
        args.task = next;
        break;
      case "agent-id":
        args.agentId = next;
        break;
      case "spawn-id":
        args.spawnId = next;
        break;
      case "working-dir":
        args.workingDir = next;
        break;
      case "max-turns":
        args.maxTurns = Number.parseInt(next, 10) || 0;
        break;
      case "max-cost-usd":
        args.maxCostUsd = Number.parseFloat(next) || 0;
        break;
      case "control-port":
        args.controlPort = Number.parseInt(next, 10) || 0;
        break;
      case "control-file":
        args.controlFile = next;
        break;
      default:
        break;
    }
  }
  if (args.controlFile) {
    args.multiTurn = true;
  }
  return args;
}

// node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs
var import_path = require("path");
var import_url = require("url");
var import_events = require("events");
var import_child_process = require("child_process");
var import_readline = require("readline");
var import_os = require("os");
var import_path2 = require("path");
var import_crypto = require("crypto");
var import_promises = require("fs/promises");
var import_path3 = require("path");
var import_fs = require("fs");
var import_process = require("process");
var import_crypto2 = require("crypto");
var import_promises2 = require("fs/promises");
var import_path4 = require("path");
var r = __toESM(require("fs"), 1);
var import_promises3 = require("fs/promises");
var import_child_process2 = require("child_process");
var import_util = require("util");
var IL = Object.create;
var { getPrototypeOf: AL, defineProperty: sQ, getOwnPropertyNames: bL } = Object;
var PL = Object.prototype.hasOwnProperty;
function ZL($) {
  return this[$];
}
var EL;
var RL;
var uU = ($, X, J) => {
  var Q = $ != null && typeof $ === "object";
  if (Q) {
    var Y = X ? EL ??= /* @__PURE__ */ new WeakMap() : RL ??= /* @__PURE__ */ new WeakMap(), z = Y.get($);
    if (z) return z;
  }
  J = $ != null ? IL(AL($)) : {};
  let W = X || !$ || !$.__esModule ? sQ(J, "default", { value: $, enumerable: true }) : J;
  for (let G of bL($)) if (!PL.call(W, G)) sQ(W, G, { get: ZL.bind($, G), enumerable: true });
  if (Q) Y.set($, W);
  return W;
};
var k = ($, X) => () => (X || $((X = { exports: {} }).exports, X), X.exports);
var SL = ($) => $;
function vL($, X) {
  this[$] = SL.bind(null, X);
}
var $1 = ($, X) => {
  for (var J in X) sQ($, J, { get: X[J], enumerable: true, configurable: true, set: vL.bind(X, J) });
};
var CL = Symbol.dispose || Symbol.for("Symbol.dispose");
var kL = Symbol.asyncDispose || Symbol.for("Symbol.asyncDispose");
var N$ = ($, X, J) => {
  if (X != null) {
    if (typeof X !== "object" && typeof X !== "function") throw TypeError('Object expected to be assigned to "using" declaration');
    var Q;
    if (J) Q = X[kL];
    if (Q === void 0) Q = X[CL];
    if (typeof Q !== "function") throw TypeError("Object not disposable");
    $.push([J, Q, X]);
  } else if (J) $.push([J]);
  return X;
};
var V$ = ($, X, J) => {
  var Q = typeof SuppressedError === "function" ? SuppressedError : function(W, G, U, H) {
    return H = Error(U), H.name = "SuppressedError", H.error = W, H.suppressed = G, H;
  }, Y = (W) => X = J ? new Q(W, X, "An error was suppressed during disposal") : (J = true, W), z = (W) => {
    while (W = $.pop()) try {
      var G = W[1] && W[1].call(W[2]);
      if (W[0]) return Promise.resolve(G).then(z, (U) => (Y(U), z()));
    } catch (U) {
      Y(U);
    }
    if (J) throw X;
  };
  return z();
};
var A9 = k((wO) => {
  Object.defineProperty(wO, "__esModule", { value: true });
  wO.regexpCode = wO.getEsmExportName = wO.getProperty = wO.safeStringify = wO.stringify = wO.strConcat = wO.addCodeArg = wO.str = wO._ = wO.nil = wO._Code = wO.Name = wO.IDENTIFIER = wO._CodeOrName = void 0;
  class BQ {
  }
  wO._CodeOrName = BQ;
  wO.IDENTIFIER = /^[a-z$_][a-z$_0-9]*$/i;
  class y0 extends BQ {
    constructor($) {
      super();
      if (!wO.IDENTIFIER.test($)) throw Error("CodeGen: name must be a valid identifier");
      this.str = $;
    }
    toString() {
      return this.str;
    }
    emptyStr() {
      return false;
    }
    get names() {
      return { [this.str]: 1 };
    }
  }
  wO.Name = y0;
  class x6 extends BQ {
    constructor($) {
      super();
      this._items = typeof $ === "string" ? [$] : $;
    }
    toString() {
      return this.str;
    }
    emptyStr() {
      if (this._items.length > 1) return false;
      let $ = this._items[0];
      return $ === "" || $ === '""';
    }
    get str() {
      var $;
      return ($ = this._str) !== null && $ !== void 0 ? $ : this._str = this._items.reduce((X, J) => `${X}${J}`, "");
    }
    get names() {
      var $;
      return ($ = this._names) !== null && $ !== void 0 ? $ : this._names = this._items.reduce((X, J) => {
        if (J instanceof y0) X[J.str] = (X[J.str] || 0) + 1;
        return X;
      }, {});
    }
  }
  wO._Code = x6;
  wO.nil = new x6("");
  function VO($, ...X) {
    let J = [$[0]], Q = 0;
    while (Q < X.length) b3(J, X[Q]), J.push($[++Q]);
    return new x6(J);
  }
  wO._ = VO;
  var A3 = new x6("+");
  function OO($, ...X) {
    let J = [I9($[0])], Q = 0;
    while (Q < X.length) J.push(A3), b3(J, X[Q]), J.push(A3, I9($[++Q]));
    return LZ(J), new x6(J);
  }
  wO.str = OO;
  function b3($, X) {
    if (X instanceof x6) $.push(...X._items);
    else if (X instanceof y0) $.push(X);
    else $.push(MZ(X));
  }
  wO.addCodeArg = b3;
  function LZ($) {
    let X = 1;
    while (X < $.length - 1) {
      if ($[X] === A3) {
        let J = jZ($[X - 1], $[X + 1]);
        if (J !== void 0) {
          $.splice(X - 1, 3, J);
          continue;
        }
        $[X++] = "+";
      }
      X++;
    }
  }
  function jZ($, X) {
    if (X === '""') return $;
    if ($ === '""') return X;
    if (typeof $ == "string") {
      if (X instanceof y0 || $[$.length - 1] !== '"') return;
      if (typeof X != "string") return `${$.slice(0, -1)}${X}"`;
      if (X[0] === '"') return $.slice(0, -1) + X.slice(1);
      return;
    }
    if (typeof X == "string" && X[0] === '"' && !($ instanceof y0)) return `"${$}${X.slice(1)}`;
    return;
  }
  function FZ($, X) {
    return X.emptyStr() ? $ : $.emptyStr() ? X : OO`${$}${X}`;
  }
  wO.strConcat = FZ;
  function MZ($) {
    return typeof $ == "number" || typeof $ == "boolean" || $ === null ? $ : I9(Array.isArray($) ? $.join(",") : $);
  }
  function IZ($) {
    return new x6(I9($));
  }
  wO.stringify = IZ;
  function I9($) {
    return JSON.stringify($).replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
  }
  wO.safeStringify = I9;
  function AZ($) {
    return typeof $ == "string" && wO.IDENTIFIER.test($) ? new x6(`.${$}`) : VO`[${$}]`;
  }
  wO.getProperty = AZ;
  function bZ($) {
    if (typeof $ == "string" && wO.IDENTIFIER.test($)) return new x6(`${$}`);
    throw Error(`CodeGen: invalid export name: ${$}, use explicit $id name mapping`);
  }
  wO.getEsmExportName = bZ;
  function PZ($) {
    return new x6($.toString());
  }
  wO.regexpCode = PZ;
});
var R3 = k((LO) => {
  Object.defineProperty(LO, "__esModule", { value: true });
  LO.ValueScope = LO.ValueScopeName = LO.Scope = LO.varKinds = LO.UsedValueState = void 0;
  var Y6 = A9();
  class qO extends Error {
    constructor($) {
      super(`CodeGen: "code" for ${$} not defined`);
      this.value = $.value;
    }
  }
  var DQ;
  (function($) {
    $[$.Started = 0] = "Started", $[$.Completed = 1] = "Completed";
  })(DQ || (LO.UsedValueState = DQ = {}));
  LO.varKinds = { const: new Y6.Name("const"), let: new Y6.Name("let"), var: new Y6.Name("var") };
  class Z3 {
    constructor({ prefixes: $, parent: X } = {}) {
      this._names = {}, this._prefixes = $, this._parent = X;
    }
    toName($) {
      return $ instanceof Y6.Name ? $ : this.name($);
    }
    name($) {
      return new Y6.Name(this._newName($));
    }
    _newName($) {
      let X = this._names[$] || this._nameGroup($);
      return `${$}${X.index++}`;
    }
    _nameGroup($) {
      var X, J;
      if (((J = (X = this._parent) === null || X === void 0 ? void 0 : X._prefixes) === null || J === void 0 ? void 0 : J.has($)) || this._prefixes && !this._prefixes.has($)) throw Error(`CodeGen: prefix "${$}" is not allowed in this scope`);
      return this._names[$] = { prefix: $, index: 0 };
    }
  }
  LO.Scope = Z3;
  class E3 extends Y6.Name {
    constructor($, X) {
      super(X);
      this.prefix = $;
    }
    setValue($, { property: X, itemIndex: J }) {
      this.value = $, this.scopePath = Y6._`.${new Y6.Name(X)}[${J}]`;
    }
  }
  LO.ValueScopeName = E3;
  var gZ = Y6._`\n`;
  class DO extends Z3 {
    constructor($) {
      super($);
      this._values = {}, this._scope = $.scope, this.opts = { ...$, _n: $.lines ? gZ : Y6.nil };
    }
    get() {
      return this._scope;
    }
    name($) {
      return new E3($, this._newName($));
    }
    value($, X) {
      var J;
      if (X.ref === void 0) throw Error("CodeGen: ref must be passed in value");
      let Q = this.toName($), { prefix: Y } = Q, z = (J = X.key) !== null && J !== void 0 ? J : X.ref, W = this._values[Y];
      if (W) {
        let H = W.get(z);
        if (H) return H;
      } else W = this._values[Y] = /* @__PURE__ */ new Map();
      W.set(z, Q);
      let G = this._scope[Y] || (this._scope[Y] = []), U = G.length;
      return G[U] = X.ref, Q.setValue(X, { property: Y, itemIndex: U }), Q;
    }
    getValue($, X) {
      let J = this._values[$];
      if (!J) return;
      return J.get(X);
    }
    scopeRefs($, X = this._values) {
      return this._reduceValues(X, (J) => {
        if (J.scopePath === void 0) throw Error(`CodeGen: name "${J}" has no value`);
        return Y6._`${$}${J.scopePath}`;
      });
    }
    scopeCode($ = this._values, X, J) {
      return this._reduceValues($, (Q) => {
        if (Q.value === void 0) throw Error(`CodeGen: name "${Q}" has no value`);
        return Q.value.code;
      }, X, J);
    }
    _reduceValues($, X, J = {}, Q) {
      let Y = Y6.nil;
      for (let z in $) {
        let W = $[z];
        if (!W) continue;
        let G = J[z] = J[z] || /* @__PURE__ */ new Map();
        W.forEach((U) => {
          if (G.has(U)) return;
          G.set(U, DQ.Started);
          let H = X(U);
          if (H) {
            let K = this.opts.es5 ? LO.varKinds.var : LO.varKinds.const;
            Y = Y6._`${Y}${K} ${U} = ${H};${this.opts._n}`;
          } else if (H = Q === null || Q === void 0 ? void 0 : Q(U)) Y = Y6._`${Y}${H}${this.opts._n}`;
          else throw new qO(U);
          G.set(U, DQ.Completed);
        });
      }
      return Y;
    }
  }
  LO.ValueScope = DO;
});
var a = k((Q6) => {
  Object.defineProperty(Q6, "__esModule", { value: true });
  Q6.or = Q6.and = Q6.not = Q6.CodeGen = Q6.operators = Q6.varKinds = Q6.ValueScopeName = Q6.ValueScope = Q6.Scope = Q6.Name = Q6.regexpCode = Q6.stringify = Q6.getProperty = Q6.nil = Q6.strConcat = Q6.str = Q6._ = void 0;
  var Y$ = A9(), T6 = R3(), c4 = A9();
  Object.defineProperty(Q6, "_", { enumerable: true, get: function() {
    return c4._;
  } });
  Object.defineProperty(Q6, "str", { enumerable: true, get: function() {
    return c4.str;
  } });
  Object.defineProperty(Q6, "strConcat", { enumerable: true, get: function() {
    return c4.strConcat;
  } });
  Object.defineProperty(Q6, "nil", { enumerable: true, get: function() {
    return c4.nil;
  } });
  Object.defineProperty(Q6, "getProperty", { enumerable: true, get: function() {
    return c4.getProperty;
  } });
  Object.defineProperty(Q6, "stringify", { enumerable: true, get: function() {
    return c4.stringify;
  } });
  Object.defineProperty(Q6, "regexpCode", { enumerable: true, get: function() {
    return c4.regexpCode;
  } });
  Object.defineProperty(Q6, "Name", { enumerable: true, get: function() {
    return c4.Name;
  } });
  var AQ = R3();
  Object.defineProperty(Q6, "Scope", { enumerable: true, get: function() {
    return AQ.Scope;
  } });
  Object.defineProperty(Q6, "ValueScope", { enumerable: true, get: function() {
    return AQ.ValueScope;
  } });
  Object.defineProperty(Q6, "ValueScopeName", { enumerable: true, get: function() {
    return AQ.ValueScopeName;
  } });
  Object.defineProperty(Q6, "varKinds", { enumerable: true, get: function() {
    return AQ.varKinds;
  } });
  Q6.operators = { GT: new Y$._Code(">"), GTE: new Y$._Code(">="), LT: new Y$._Code("<"), LTE: new Y$._Code("<="), EQ: new Y$._Code("==="), NEQ: new Y$._Code("!=="), NOT: new Y$._Code("!"), OR: new Y$._Code("||"), AND: new Y$._Code("&&"), ADD: new Y$._Code("+") };
  class p4 {
    optimizeNodes() {
      return this;
    }
    optimizeNames($, X) {
      return this;
    }
  }
  class FO extends p4 {
    constructor($, X, J) {
      super();
      this.varKind = $, this.name = X, this.rhs = J;
    }
    render({ es5: $, _n: X }) {
      let J = $ ? T6.varKinds.var : this.varKind, Q = this.rhs === void 0 ? "" : ` = ${this.rhs}`;
      return `${J} ${this.name}${Q};` + X;
    }
    optimizeNames($, X) {
      if (!$[this.name.str]) return;
      if (this.rhs) this.rhs = g0(this.rhs, $, X);
      return this;
    }
    get names() {
      return this.rhs instanceof Y$._CodeOrName ? this.rhs.names : {};
    }
  }
  class C3 extends p4 {
    constructor($, X, J) {
      super();
      this.lhs = $, this.rhs = X, this.sideEffects = J;
    }
    render({ _n: $ }) {
      return `${this.lhs} = ${this.rhs};` + $;
    }
    optimizeNames($, X) {
      if (this.lhs instanceof Y$.Name && !$[this.lhs.str] && !this.sideEffects) return;
      return this.rhs = g0(this.rhs, $, X), this;
    }
    get names() {
      let $ = this.lhs instanceof Y$.Name ? {} : { ...this.lhs.names };
      return IQ($, this.rhs);
    }
  }
  class MO extends C3 {
    constructor($, X, J, Q) {
      super($, J, Q);
      this.op = X;
    }
    render({ _n: $ }) {
      return `${this.lhs} ${this.op}= ${this.rhs};` + $;
    }
  }
  class IO extends p4 {
    constructor($) {
      super();
      this.label = $, this.names = {};
    }
    render({ _n: $ }) {
      return `${this.label}:` + $;
    }
  }
  class AO extends p4 {
    constructor($) {
      super();
      this.label = $, this.names = {};
    }
    render({ _n: $ }) {
      return `break${this.label ? ` ${this.label}` : ""};` + $;
    }
  }
  class bO extends p4 {
    constructor($) {
      super();
      this.error = $;
    }
    render({ _n: $ }) {
      return `throw ${this.error};` + $;
    }
    get names() {
      return this.error.names;
    }
  }
  class PO extends p4 {
    constructor($) {
      super();
      this.code = $;
    }
    render({ _n: $ }) {
      return `${this.code};` + $;
    }
    optimizeNodes() {
      return `${this.code}` ? this : void 0;
    }
    optimizeNames($, X) {
      return this.code = g0(this.code, $, X), this;
    }
    get names() {
      return this.code instanceof Y$._CodeOrName ? this.code.names : {};
    }
  }
  class bQ extends p4 {
    constructor($ = []) {
      super();
      this.nodes = $;
    }
    render($) {
      return this.nodes.reduce((X, J) => X + J.render($), "");
    }
    optimizeNodes() {
      let { nodes: $ } = this, X = $.length;
      while (X--) {
        let J = $[X].optimizeNodes();
        if (Array.isArray(J)) $.splice(X, 1, ...J);
        else if (J) $[X] = J;
        else $.splice(X, 1);
      }
      return $.length > 0 ? this : void 0;
    }
    optimizeNames($, X) {
      let { nodes: J } = this, Q = J.length;
      while (Q--) {
        let Y = J[Q];
        if (Y.optimizeNames($, X)) continue;
        lZ($, Y.names), J.splice(Q, 1);
      }
      return J.length > 0 ? this : void 0;
    }
    get names() {
      return this.nodes.reduce(($, X) => E1($, X.names), {});
    }
  }
  class i4 extends bQ {
    render($) {
      return "{" + $._n + super.render($) + "}" + $._n;
    }
  }
  class ZO extends bQ {
  }
  class b9 extends i4 {
  }
  b9.kind = "else";
  class w4 extends i4 {
    constructor($, X) {
      super(X);
      this.condition = $;
    }
    render($) {
      let X = `if(${this.condition})` + super.render($);
      if (this.else) X += "else " + this.else.render($);
      return X;
    }
    optimizeNodes() {
      super.optimizeNodes();
      let $ = this.condition;
      if ($ === true) return this.nodes;
      let X = this.else;
      if (X) {
        let J = X.optimizeNodes();
        X = this.else = Array.isArray(J) ? new b9(J) : J;
      }
      if (X) {
        if ($ === false) return X instanceof w4 ? X : X.nodes;
        if (this.nodes.length) return this;
        return new w4(CO($), X instanceof w4 ? [X] : X.nodes);
      }
      if ($ === false || !this.nodes.length) return;
      return this;
    }
    optimizeNames($, X) {
      var J;
      if (this.else = (J = this.else) === null || J === void 0 ? void 0 : J.optimizeNames($, X), !(super.optimizeNames($, X) || this.else)) return;
      return this.condition = g0(this.condition, $, X), this;
    }
    get names() {
      let $ = super.names;
      if (IQ($, this.condition), this.else) E1($, this.else.names);
      return $;
    }
  }
  w4.kind = "if";
  class f0 extends i4 {
  }
  f0.kind = "for";
  class EO extends f0 {
    constructor($) {
      super();
      this.iteration = $;
    }
    render($) {
      return `for(${this.iteration})` + super.render($);
    }
    optimizeNames($, X) {
      if (!super.optimizeNames($, X)) return;
      return this.iteration = g0(this.iteration, $, X), this;
    }
    get names() {
      return E1(super.names, this.iteration.names);
    }
  }
  class RO extends f0 {
    constructor($, X, J, Q) {
      super();
      this.varKind = $, this.name = X, this.from = J, this.to = Q;
    }
    render($) {
      let X = $.es5 ? T6.varKinds.var : this.varKind, { name: J, from: Q, to: Y } = this;
      return `for(${X} ${J}=${Q}; ${J}<${Y}; ${J}++)` + super.render($);
    }
    get names() {
      let $ = IQ(super.names, this.from);
      return IQ($, this.to);
    }
  }
  class S3 extends f0 {
    constructor($, X, J, Q) {
      super();
      this.loop = $, this.varKind = X, this.name = J, this.iterable = Q;
    }
    render($) {
      return `for(${this.varKind} ${this.name} ${this.loop} ${this.iterable})` + super.render($);
    }
    optimizeNames($, X) {
      if (!super.optimizeNames($, X)) return;
      return this.iterable = g0(this.iterable, $, X), this;
    }
    get names() {
      return E1(super.names, this.iterable.names);
    }
  }
  class LQ extends i4 {
    constructor($, X, J) {
      super();
      this.name = $, this.args = X, this.async = J;
    }
    render($) {
      return `${this.async ? "async " : ""}function ${this.name}(${this.args})` + super.render($);
    }
  }
  LQ.kind = "func";
  class jQ extends bQ {
    render($) {
      return "return " + super.render($);
    }
  }
  jQ.kind = "return";
  class SO extends i4 {
    render($) {
      let X = "try" + super.render($);
      if (this.catch) X += this.catch.render($);
      if (this.finally) X += this.finally.render($);
      return X;
    }
    optimizeNodes() {
      var $, X;
      return super.optimizeNodes(), ($ = this.catch) === null || $ === void 0 || $.optimizeNodes(), (X = this.finally) === null || X === void 0 || X.optimizeNodes(), this;
    }
    optimizeNames($, X) {
      var J, Q;
      return super.optimizeNames($, X), (J = this.catch) === null || J === void 0 || J.optimizeNames($, X), (Q = this.finally) === null || Q === void 0 || Q.optimizeNames($, X), this;
    }
    get names() {
      let $ = super.names;
      if (this.catch) E1($, this.catch.names);
      if (this.finally) E1($, this.finally.names);
      return $;
    }
  }
  class FQ extends i4 {
    constructor($) {
      super();
      this.error = $;
    }
    render($) {
      return `catch(${this.error})` + super.render($);
    }
  }
  FQ.kind = "catch";
  class MQ extends i4 {
    render($) {
      return "finally" + super.render($);
    }
  }
  MQ.kind = "finally";
  class vO {
    constructor($, X = {}) {
      this._values = {}, this._blockStarts = [], this._constants = {}, this.opts = { ...X, _n: X.lines ? `
` : "" }, this._extScope = $, this._scope = new T6.Scope({ parent: $ }), this._nodes = [new ZO()];
    }
    toString() {
      return this._root.render(this.opts);
    }
    name($) {
      return this._scope.name($);
    }
    scopeName($) {
      return this._extScope.name($);
    }
    scopeValue($, X) {
      let J = this._extScope.value($, X);
      return (this._values[J.prefix] || (this._values[J.prefix] = /* @__PURE__ */ new Set())).add(J), J;
    }
    getScopeValue($, X) {
      return this._extScope.getValue($, X);
    }
    scopeRefs($) {
      return this._extScope.scopeRefs($, this._values);
    }
    scopeCode() {
      return this._extScope.scopeCode(this._values);
    }
    _def($, X, J, Q) {
      let Y = this._scope.toName(X);
      if (J !== void 0 && Q) this._constants[Y.str] = J;
      return this._leafNode(new FO($, Y, J)), Y;
    }
    const($, X, J) {
      return this._def(T6.varKinds.const, $, X, J);
    }
    let($, X, J) {
      return this._def(T6.varKinds.let, $, X, J);
    }
    var($, X, J) {
      return this._def(T6.varKinds.var, $, X, J);
    }
    assign($, X, J) {
      return this._leafNode(new C3($, X, J));
    }
    add($, X) {
      return this._leafNode(new MO($, Q6.operators.ADD, X));
    }
    code($) {
      if (typeof $ == "function") $();
      else if ($ !== Y$.nil) this._leafNode(new PO($));
      return this;
    }
    object(...$) {
      let X = ["{"];
      for (let [J, Q] of $) {
        if (X.length > 1) X.push(",");
        if (X.push(J), J !== Q || this.opts.es5) X.push(":"), (0, Y$.addCodeArg)(X, Q);
      }
      return X.push("}"), new Y$._Code(X);
    }
    if($, X, J) {
      if (this._blockNode(new w4($)), X && J) this.code(X).else().code(J).endIf();
      else if (X) this.code(X).endIf();
      else if (J) throw Error('CodeGen: "else" body without "then" body');
      return this;
    }
    elseIf($) {
      return this._elseNode(new w4($));
    }
    else() {
      return this._elseNode(new b9());
    }
    endIf() {
      return this._endBlockNode(w4, b9);
    }
    _for($, X) {
      if (this._blockNode($), X) this.code(X).endFor();
      return this;
    }
    for($, X) {
      return this._for(new EO($), X);
    }
    forRange($, X, J, Q, Y = this.opts.es5 ? T6.varKinds.var : T6.varKinds.let) {
      let z = this._scope.toName($);
      return this._for(new RO(Y, z, X, J), () => Q(z));
    }
    forOf($, X, J, Q = T6.varKinds.const) {
      let Y = this._scope.toName($);
      if (this.opts.es5) {
        let z = X instanceof Y$.Name ? X : this.var("_arr", X);
        return this.forRange("_i", 0, Y$._`${z}.length`, (W) => {
          this.var(Y, Y$._`${z}[${W}]`), J(Y);
        });
      }
      return this._for(new S3("of", Q, Y, X), () => J(Y));
    }
    forIn($, X, J, Q = this.opts.es5 ? T6.varKinds.var : T6.varKinds.const) {
      if (this.opts.ownProperties) return this.forOf($, Y$._`Object.keys(${X})`, J);
      let Y = this._scope.toName($);
      return this._for(new S3("in", Q, Y, X), () => J(Y));
    }
    endFor() {
      return this._endBlockNode(f0);
    }
    label($) {
      return this._leafNode(new IO($));
    }
    break($) {
      return this._leafNode(new AO($));
    }
    return($) {
      let X = new jQ();
      if (this._blockNode(X), this.code($), X.nodes.length !== 1) throw Error('CodeGen: "return" should have one node');
      return this._endBlockNode(jQ);
    }
    try($, X, J) {
      if (!X && !J) throw Error('CodeGen: "try" without "catch" and "finally"');
      let Q = new SO();
      if (this._blockNode(Q), this.code($), X) {
        let Y = this.name("e");
        this._currNode = Q.catch = new FQ(Y), X(Y);
      }
      if (J) this._currNode = Q.finally = new MQ(), this.code(J);
      return this._endBlockNode(FQ, MQ);
    }
    throw($) {
      return this._leafNode(new bO($));
    }
    block($, X) {
      if (this._blockStarts.push(this._nodes.length), $) this.code($).endBlock(X);
      return this;
    }
    endBlock($) {
      let X = this._blockStarts.pop();
      if (X === void 0) throw Error("CodeGen: not in self-balancing block");
      let J = this._nodes.length - X;
      if (J < 0 || $ !== void 0 && J !== $) throw Error(`CodeGen: wrong number of nodes: ${J} vs ${$} expected`);
      return this._nodes.length = X, this;
    }
    func($, X = Y$.nil, J, Q) {
      if (this._blockNode(new LQ($, X, J)), Q) this.code(Q).endFunc();
      return this;
    }
    endFunc() {
      return this._endBlockNode(LQ);
    }
    optimize($ = 1) {
      while ($-- > 0) this._root.optimizeNodes(), this._root.optimizeNames(this._root.names, this._constants);
    }
    _leafNode($) {
      return this._currNode.nodes.push($), this;
    }
    _blockNode($) {
      this._currNode.nodes.push($), this._nodes.push($);
    }
    _endBlockNode($, X) {
      let J = this._currNode;
      if (J instanceof $ || X && J instanceof X) return this._nodes.pop(), this;
      throw Error(`CodeGen: not in block "${X ? `${$.kind}/${X.kind}` : $.kind}"`);
    }
    _elseNode($) {
      let X = this._currNode;
      if (!(X instanceof w4)) throw Error('CodeGen: "else" without "if"');
      return this._currNode = X.else = $, this;
    }
    get _root() {
      return this._nodes[0];
    }
    get _currNode() {
      let $ = this._nodes;
      return $[$.length - 1];
    }
    set _currNode($) {
      let X = this._nodes;
      X[X.length - 1] = $;
    }
  }
  Q6.CodeGen = vO;
  function E1($, X) {
    for (let J in X) $[J] = ($[J] || 0) + (X[J] || 0);
    return $;
  }
  function IQ($, X) {
    return X instanceof Y$._CodeOrName ? E1($, X.names) : $;
  }
  function g0($, X, J) {
    if ($ instanceof Y$.Name) return Q($);
    if (!Y($)) return $;
    return new Y$._Code($._items.reduce((z, W) => {
      if (W instanceof Y$.Name) W = Q(W);
      if (W instanceof Y$._Code) z.push(...W._items);
      else z.push(W);
      return z;
    }, []));
    function Q(z) {
      let W = J[z.str];
      if (W === void 0 || X[z.str] !== 1) return z;
      return delete X[z.str], W;
    }
    function Y(z) {
      return z instanceof Y$._Code && z._items.some((W) => W instanceof Y$.Name && X[W.str] === 1 && J[W.str] !== void 0);
    }
  }
  function lZ($, X) {
    for (let J in X) $[J] = ($[J] || 0) - (X[J] || 0);
  }
  function CO($) {
    return typeof $ == "boolean" || typeof $ == "number" || $ === null ? !$ : Y$._`!${v3($)}`;
  }
  Q6.not = CO;
  var cZ = kO(Q6.operators.AND);
  function pZ(...$) {
    return $.reduce(cZ);
  }
  Q6.and = pZ;
  var iZ = kO(Q6.operators.OR);
  function nZ(...$) {
    return $.reduce(iZ);
  }
  Q6.or = nZ;
  function kO($) {
    return (X, J) => X === Y$.nil ? J : J === Y$.nil ? X : Y$._`${v3(X)} ${$} ${v3(J)}`;
  }
  function v3($) {
    return $ instanceof Y$.Name ? $ : Y$._`(${$})`;
  }
});
var Q$ = k((mO) => {
  Object.defineProperty(mO, "__esModule", { value: true });
  mO.checkStrictMode = mO.getErrorPath = mO.Type = mO.useFunc = mO.setEvaluated = mO.evaluatedPropsToName = mO.mergeEvaluated = mO.eachItem = mO.unescapeJsonPointer = mO.escapeJsonPointer = mO.escapeFragment = mO.unescapeFragment = mO.schemaRefOrVal = mO.schemaHasRulesButRef = mO.schemaHasRules = mO.checkUnknownRules = mO.alwaysValidSchema = mO.toHash = void 0;
  var K$ = a(), tZ = A9();
  function aZ($) {
    let X = {};
    for (let J of $) X[J] = true;
    return X;
  }
  mO.toHash = aZ;
  function sZ($, X) {
    if (typeof X == "boolean") return X;
    if (Object.keys(X).length === 0) return true;
    return yO($, X), !fO(X, $.self.RULES.all);
  }
  mO.alwaysValidSchema = sZ;
  function yO($, X = $.schema) {
    let { opts: J, self: Q } = $;
    if (!J.strictSchema) return;
    if (typeof X === "boolean") return;
    let Y = Q.RULES.keywords;
    for (let z in X) if (!Y[z]) uO($, `unknown keyword: "${z}"`);
  }
  mO.checkUnknownRules = yO;
  function fO($, X) {
    if (typeof $ == "boolean") return !$;
    for (let J in $) if (X[J]) return true;
    return false;
  }
  mO.schemaHasRules = fO;
  function eZ($, X) {
    if (typeof $ == "boolean") return !$;
    for (let J in $) if (J !== "$ref" && X.all[J]) return true;
    return false;
  }
  mO.schemaHasRulesButRef = eZ;
  function $E({ topSchemaRef: $, schemaPath: X }, J, Q, Y) {
    if (!Y) {
      if (typeof J == "number" || typeof J == "boolean") return J;
      if (typeof J == "string") return K$._`${J}`;
    }
    return K$._`${$}${X}${(0, K$.getProperty)(Q)}`;
  }
  mO.schemaRefOrVal = $E;
  function XE($) {
    return gO(decodeURIComponent($));
  }
  mO.unescapeFragment = XE;
  function JE($) {
    return encodeURIComponent(_3($));
  }
  mO.escapeFragment = JE;
  function _3($) {
    if (typeof $ == "number") return `${$}`;
    return $.replace(/~/g, "~0").replace(/\//g, "~1");
  }
  mO.escapeJsonPointer = _3;
  function gO($) {
    return $.replace(/~1/g, "/").replace(/~0/g, "~");
  }
  mO.unescapeJsonPointer = gO;
  function YE($, X) {
    if (Array.isArray($)) for (let J of $) X(J);
    else X($);
  }
  mO.eachItem = YE;
  function xO({ mergeNames: $, mergeToName: X, mergeValues: J, resultToName: Q }) {
    return (Y, z, W, G) => {
      let U = W === void 0 ? z : W instanceof K$.Name ? (z instanceof K$.Name ? $(Y, z, W) : X(Y, z, W), W) : z instanceof K$.Name ? (X(Y, W, z), z) : J(z, W);
      return G === K$.Name && !(U instanceof K$.Name) ? Q(Y, U) : U;
    };
  }
  mO.mergeEvaluated = { props: xO({ mergeNames: ($, X, J) => $.if(K$._`${J} !== true && ${X} !== undefined`, () => {
    $.if(K$._`${X} === true`, () => $.assign(J, true), () => $.assign(J, K$._`${J} || {}`).code(K$._`Object.assign(${J}, ${X})`));
  }), mergeToName: ($, X, J) => $.if(K$._`${J} !== true`, () => {
    if (X === true) $.assign(J, true);
    else $.assign(J, K$._`${J} || {}`), x3($, J, X);
  }), mergeValues: ($, X) => $ === true ? true : { ...$, ...X }, resultToName: hO }), items: xO({ mergeNames: ($, X, J) => $.if(K$._`${J} !== true && ${X} !== undefined`, () => $.assign(J, K$._`${X} === true ? true : ${J} > ${X} ? ${J} : ${X}`)), mergeToName: ($, X, J) => $.if(K$._`${J} !== true`, () => $.assign(J, X === true ? true : K$._`${J} > ${X} ? ${J} : ${X}`)), mergeValues: ($, X) => $ === true ? true : Math.max($, X), resultToName: ($, X) => $.var("items", X) }) };
  function hO($, X) {
    if (X === true) return $.var("props", true);
    let J = $.var("props", K$._`{}`);
    if (X !== void 0) x3($, J, X);
    return J;
  }
  mO.evaluatedPropsToName = hO;
  function x3($, X, J) {
    Object.keys(J).forEach((Q) => $.assign(K$._`${X}${(0, K$.getProperty)(Q)}`, true));
  }
  mO.setEvaluated = x3;
  var TO = {};
  function QE($, X) {
    return $.scopeValue("func", { ref: X, code: TO[X.code] || (TO[X.code] = new tZ._Code(X.code)) });
  }
  mO.useFunc = QE;
  var k3;
  (function($) {
    $[$.Num = 0] = "Num", $[$.Str = 1] = "Str";
  })(k3 || (mO.Type = k3 = {}));
  function zE($, X, J) {
    if ($ instanceof K$.Name) {
      let Q = X === k3.Num;
      return J ? Q ? K$._`"[" + ${$} + "]"` : K$._`"['" + ${$} + "']"` : Q ? K$._`"/" + ${$}` : K$._`"/" + ${$}.replace(/~/g, "~0").replace(/\\//g, "~1")`;
    }
    return J ? (0, K$.getProperty)($).toString() : "/" + _3($);
  }
  mO.getErrorPath = zE;
  function uO($, X, J = $.opts.strictSchema) {
    if (!J) return;
    if (X = `strict mode: ${X}`, J === true) throw Error(X);
    $.self.logger.warn(X);
  }
  mO.checkStrictMode = uO;
});
var B4 = k((cO) => {
  Object.defineProperty(cO, "__esModule", { value: true });
  var c$ = a(), AE = { data: new c$.Name("data"), valCxt: new c$.Name("valCxt"), instancePath: new c$.Name("instancePath"), parentData: new c$.Name("parentData"), parentDataProperty: new c$.Name("parentDataProperty"), rootData: new c$.Name("rootData"), dynamicAnchors: new c$.Name("dynamicAnchors"), vErrors: new c$.Name("vErrors"), errors: new c$.Name("errors"), this: new c$.Name("this"), self: new c$.Name("self"), scope: new c$.Name("scope"), json: new c$.Name("json"), jsonPos: new c$.Name("jsonPos"), jsonLen: new c$.Name("jsonLen"), jsonPart: new c$.Name("jsonPart") };
  cO.default = AE;
});
var P9 = k((dO) => {
  Object.defineProperty(dO, "__esModule", { value: true });
  dO.extendErrors = dO.resetErrorsCount = dO.reportExtraError = dO.reportError = dO.keyword$DataError = dO.keywordError = void 0;
  var z$ = a(), ZQ = Q$(), o$ = B4();
  dO.keywordError = { message: ({ keyword: $ }) => z$.str`must pass "${$}" keyword validation` };
  dO.keyword$DataError = { message: ({ keyword: $, schemaType: X }) => X ? z$.str`"${$}" keyword must be ${X} ($data)` : z$.str`"${$}" keyword is invalid ($data)` };
  function PE($, X = dO.keywordError, J, Q) {
    let { it: Y } = $, { gen: z, compositeRule: W, allErrors: G } = Y, U = nO($, X, J);
    if (Q !== null && Q !== void 0 ? Q : W || G) pO(z, U);
    else iO(Y, z$._`[${U}]`);
  }
  dO.reportError = PE;
  function ZE($, X = dO.keywordError, J) {
    let { it: Q } = $, { gen: Y, compositeRule: z, allErrors: W } = Q, G = nO($, X, J);
    if (pO(Y, G), !(z || W)) iO(Q, o$.default.vErrors);
  }
  dO.reportExtraError = ZE;
  function EE($, X) {
    $.assign(o$.default.errors, X), $.if(z$._`${o$.default.vErrors} !== null`, () => $.if(X, () => $.assign(z$._`${o$.default.vErrors}.length`, X), () => $.assign(o$.default.vErrors, null)));
  }
  dO.resetErrorsCount = EE;
  function RE({ gen: $, keyword: X, schemaValue: J, data: Q, errsCount: Y, it: z }) {
    if (Y === void 0) throw Error("ajv implementation error");
    let W = $.name("err");
    $.forRange("i", Y, o$.default.errors, (G) => {
      if ($.const(W, z$._`${o$.default.vErrors}[${G}]`), $.if(z$._`${W}.instancePath === undefined`, () => $.assign(z$._`${W}.instancePath`, (0, z$.strConcat)(o$.default.instancePath, z.errorPath))), $.assign(z$._`${W}.schemaPath`, z$.str`${z.errSchemaPath}/${X}`), z.opts.verbose) $.assign(z$._`${W}.schema`, J), $.assign(z$._`${W}.data`, Q);
    });
  }
  dO.extendErrors = RE;
  function pO($, X) {
    let J = $.const("err", X);
    $.if(z$._`${o$.default.vErrors} === null`, () => $.assign(o$.default.vErrors, z$._`[${J}]`), z$._`${o$.default.vErrors}.push(${J})`), $.code(z$._`${o$.default.errors}++`);
  }
  function iO($, X) {
    let { gen: J, validateName: Q, schemaEnv: Y } = $;
    if (Y.$async) J.throw(z$._`new ${$.ValidationError}(${X})`);
    else J.assign(z$._`${Q}.errors`, X), J.return(false);
  }
  var R1 = { keyword: new z$.Name("keyword"), schemaPath: new z$.Name("schemaPath"), params: new z$.Name("params"), propertyName: new z$.Name("propertyName"), message: new z$.Name("message"), schema: new z$.Name("schema"), parentSchema: new z$.Name("parentSchema") };
  function nO($, X, J) {
    let { createErrors: Q } = $.it;
    if (Q === false) return z$._`{}`;
    return SE($, X, J);
  }
  function SE($, X, J = {}) {
    let { gen: Q, it: Y } = $, z = [vE(Y, J), CE($, J)];
    return kE($, X, z), Q.object(...z);
  }
  function vE({ errorPath: $ }, { instancePath: X }) {
    let J = X ? z$.str`${$}${(0, ZQ.getErrorPath)(X, ZQ.Type.Str)}` : $;
    return [o$.default.instancePath, (0, z$.strConcat)(o$.default.instancePath, J)];
  }
  function CE({ keyword: $, it: { errSchemaPath: X } }, { schemaPath: J, parentSchema: Q }) {
    let Y = Q ? X : z$.str`${X}/${$}`;
    if (J) Y = z$.str`${Y}${(0, ZQ.getErrorPath)(J, ZQ.Type.Str)}`;
    return [R1.schemaPath, Y];
  }
  function kE($, { params: X, message: J }, Q) {
    let { keyword: Y, data: z, schemaValue: W, it: G } = $, { opts: U, propertyName: H, topSchemaRef: K, schemaPath: V } = G;
    if (Q.push([R1.keyword, Y], [R1.params, typeof X == "function" ? X($) : X || z$._`{}`]), U.messages) Q.push([R1.message, typeof J == "function" ? J($) : J]);
    if (U.verbose) Q.push([R1.schema, W], [R1.parentSchema, z$._`${K}${V}`], [o$.default.data, z]);
    if (H) Q.push([R1.propertyName, H]);
  }
});
var sO = k((tO) => {
  Object.defineProperty(tO, "__esModule", { value: true });
  tO.boolOrEmptySchema = tO.topBoolOrEmptySchema = void 0;
  var fE = P9(), gE = a(), hE = B4(), uE = { message: "boolean schema is false" };
  function mE($) {
    let { gen: X, schema: J, validateName: Q } = $;
    if (J === false) oO($, false);
    else if (typeof J == "object" && J.$async === true) X.return(hE.default.data);
    else X.assign(gE._`${Q}.errors`, null), X.return(true);
  }
  tO.topBoolOrEmptySchema = mE;
  function lE($, X) {
    let { gen: J, schema: Q } = $;
    if (Q === false) J.var(X, false), oO($);
    else J.var(X, true);
  }
  tO.boolOrEmptySchema = lE;
  function oO($, X) {
    let { gen: J, data: Q } = $, Y = { gen: J, keyword: "false schema", data: Q, schema: false, schemaCode: false, schemaValue: false, params: {}, it: $ };
    (0, fE.reportError)(Y, uE, void 0, X);
  }
});
var y3 = k((eO) => {
  Object.defineProperty(eO, "__esModule", { value: true });
  eO.getRules = eO.isJSONType = void 0;
  var pE = ["string", "number", "integer", "boolean", "null", "object", "array"], iE = new Set(pE);
  function nE($) {
    return typeof $ == "string" && iE.has($);
  }
  eO.isJSONType = nE;
  function dE() {
    let $ = { number: { type: "number", rules: [] }, string: { type: "string", rules: [] }, array: { type: "array", rules: [] }, object: { type: "object", rules: [] } };
    return { types: { ...$, integer: true, boolean: true, null: true }, rules: [{ rules: [] }, $.number, $.string, $.array, $.object], post: { rules: [] }, all: {}, keywords: {} };
  }
  eO.getRules = dE;
});
var f3 = k((Yw) => {
  Object.defineProperty(Yw, "__esModule", { value: true });
  Yw.shouldUseRule = Yw.shouldUseGroup = Yw.schemaHasRulesForType = void 0;
  function oE({ schema: $, self: X }, J) {
    let Q = X.RULES.types[J];
    return Q && Q !== true && Xw($, Q);
  }
  Yw.schemaHasRulesForType = oE;
  function Xw($, X) {
    return X.rules.some((J) => Jw($, J));
  }
  Yw.shouldUseGroup = Xw;
  function Jw($, X) {
    var J;
    return $[X.keyword] !== void 0 || ((J = X.definition.implements) === null || J === void 0 ? void 0 : J.some((Q) => $[Q] !== void 0));
  }
  Yw.shouldUseRule = Jw;
});
var Z9 = k((Uw) => {
  Object.defineProperty(Uw, "__esModule", { value: true });
  Uw.reportTypeError = Uw.checkDataTypes = Uw.checkDataType = Uw.coerceAndCheckDataType = Uw.getJSONTypes = Uw.getSchemaTypes = Uw.DataType = void 0;
  var sE = y3(), eE = f3(), $R = P9(), t = a(), zw = Q$(), h0;
  (function($) {
    $[$.Correct = 0] = "Correct", $[$.Wrong = 1] = "Wrong";
  })(h0 || (Uw.DataType = h0 = {}));
  function XR($) {
    let X = Ww($.type);
    if (X.includes("null")) {
      if ($.nullable === false) throw Error("type: null contradicts nullable: false");
    } else {
      if (!X.length && $.nullable !== void 0) throw Error('"nullable" cannot be used without "type"');
      if ($.nullable === true) X.push("null");
    }
    return X;
  }
  Uw.getSchemaTypes = XR;
  function Ww($) {
    let X = Array.isArray($) ? $ : $ ? [$] : [];
    if (X.every(sE.isJSONType)) return X;
    throw Error("type must be JSONType or JSONType[]: " + X.join(","));
  }
  Uw.getJSONTypes = Ww;
  function JR($, X) {
    let { gen: J, data: Q, opts: Y } = $, z = YR(X, Y.coerceTypes), W = X.length > 0 && !(z.length === 0 && X.length === 1 && (0, eE.schemaHasRulesForType)($, X[0]));
    if (W) {
      let G = h3(X, Q, Y.strictNumbers, h0.Wrong);
      J.if(G, () => {
        if (z.length) QR($, X, z);
        else u3($);
      });
    }
    return W;
  }
  Uw.coerceAndCheckDataType = JR;
  var Gw = /* @__PURE__ */ new Set(["string", "number", "integer", "boolean", "null"]);
  function YR($, X) {
    return X ? $.filter((J) => Gw.has(J) || X === "array" && J === "array") : [];
  }
  function QR($, X, J) {
    let { gen: Q, data: Y, opts: z } = $, W = Q.let("dataType", t._`typeof ${Y}`), G = Q.let("coerced", t._`undefined`);
    if (z.coerceTypes === "array") Q.if(t._`${W} == 'object' && Array.isArray(${Y}) && ${Y}.length == 1`, () => Q.assign(Y, t._`${Y}[0]`).assign(W, t._`typeof ${Y}`).if(h3(X, Y, z.strictNumbers), () => Q.assign(G, Y)));
    Q.if(t._`${G} !== undefined`);
    for (let H of J) if (Gw.has(H) || H === "array" && z.coerceTypes === "array") U(H);
    Q.else(), u3($), Q.endIf(), Q.if(t._`${G} !== undefined`, () => {
      Q.assign(Y, G), zR($, G);
    });
    function U(H) {
      switch (H) {
        case "string":
          Q.elseIf(t._`${W} == "number" || ${W} == "boolean"`).assign(G, t._`"" + ${Y}`).elseIf(t._`${Y} === null`).assign(G, t._`""`);
          return;
        case "number":
          Q.elseIf(t._`${W} == "boolean" || ${Y} === null
              || (${W} == "string" && ${Y} && ${Y} == +${Y})`).assign(G, t._`+${Y}`);
          return;
        case "integer":
          Q.elseIf(t._`${W} === "boolean" || ${Y} === null
              || (${W} === "string" && ${Y} && ${Y} == +${Y} && !(${Y} % 1))`).assign(G, t._`+${Y}`);
          return;
        case "boolean":
          Q.elseIf(t._`${Y} === "false" || ${Y} === 0 || ${Y} === null`).assign(G, false).elseIf(t._`${Y} === "true" || ${Y} === 1`).assign(G, true);
          return;
        case "null":
          Q.elseIf(t._`${Y} === "" || ${Y} === 0 || ${Y} === false`), Q.assign(G, null);
          return;
        case "array":
          Q.elseIf(t._`${W} === "string" || ${W} === "number"
              || ${W} === "boolean" || ${Y} === null`).assign(G, t._`[${Y}]`);
      }
    }
  }
  function zR({ gen: $, parentData: X, parentDataProperty: J }, Q) {
    $.if(t._`${X} !== undefined`, () => $.assign(t._`${X}[${J}]`, Q));
  }
  function g3($, X, J, Q = h0.Correct) {
    let Y = Q === h0.Correct ? t.operators.EQ : t.operators.NEQ, z;
    switch ($) {
      case "null":
        return t._`${X} ${Y} null`;
      case "array":
        z = t._`Array.isArray(${X})`;
        break;
      case "object":
        z = t._`${X} && typeof ${X} == "object" && !Array.isArray(${X})`;
        break;
      case "integer":
        z = W(t._`!(${X} % 1) && !isNaN(${X})`);
        break;
      case "number":
        z = W();
        break;
      default:
        return t._`typeof ${X} ${Y} ${$}`;
    }
    return Q === h0.Correct ? z : (0, t.not)(z);
    function W(G = t.nil) {
      return (0, t.and)(t._`typeof ${X} == "number"`, G, J ? t._`isFinite(${X})` : t.nil);
    }
  }
  Uw.checkDataType = g3;
  function h3($, X, J, Q) {
    if ($.length === 1) return g3($[0], X, J, Q);
    let Y, z = (0, zw.toHash)($);
    if (z.array && z.object) {
      let W = t._`typeof ${X} != "object"`;
      Y = z.null ? W : t._`!${X} || ${W}`, delete z.null, delete z.array, delete z.object;
    } else Y = t.nil;
    if (z.number) delete z.integer;
    for (let W in z) Y = (0, t.and)(Y, g3(W, X, J, Q));
    return Y;
  }
  Uw.checkDataTypes = h3;
  var WR = { message: ({ schema: $ }) => `must be ${$}`, params: ({ schema: $, schemaValue: X }) => typeof $ == "string" ? t._`{type: ${$}}` : t._`{type: ${X}}` };
  function u3($) {
    let X = GR($);
    (0, $R.reportError)(X, WR);
  }
  Uw.reportTypeError = u3;
  function GR($) {
    let { gen: X, data: J, schema: Q } = $, Y = (0, zw.schemaRefOrVal)($, Q, "type");
    return { gen: X, keyword: "type", data: J, schema: Q.type, schemaCode: Y, schemaValue: Y, parentSchema: Q, params: {}, it: $ };
  }
});
var Ow = k((Nw) => {
  Object.defineProperty(Nw, "__esModule", { value: true });
  Nw.assignDefaults = void 0;
  var u0 = a(), wR = Q$();
  function BR($, X) {
    let { properties: J, items: Q } = $.schema;
    if (X === "object" && J) for (let Y in J) Kw($, Y, J[Y].default);
    else if (X === "array" && Array.isArray(Q)) Q.forEach((Y, z) => Kw($, z, Y.default));
  }
  Nw.assignDefaults = BR;
  function Kw($, X, J) {
    let { gen: Q, compositeRule: Y, data: z, opts: W } = $;
    if (J === void 0) return;
    let G = u0._`${z}${(0, u0.getProperty)(X)}`;
    if (Y) {
      (0, wR.checkStrictMode)($, `default is ignored for: ${G}`);
      return;
    }
    let U = u0._`${G} === undefined`;
    if (W.useDefaults === "empty") U = u0._`${U} || ${G} === null || ${G} === ""`;
    Q.if(U, u0._`${G} = ${(0, u0.stringify)(J)}`);
  }
});
var Z6 = k((qw) => {
  Object.defineProperty(qw, "__esModule", { value: true });
  qw.validateUnion = qw.validateArray = qw.usePattern = qw.callValidateCode = qw.schemaProperties = qw.allSchemaProperties = qw.noPropertyInData = qw.propertyInData = qw.isOwnProperty = qw.hasPropFunc = qw.reportMissingProp = qw.checkMissingProp = qw.checkReportMissingProp = void 0;
  var j$ = a(), m3 = Q$(), n4 = B4(), qR = Q$();
  function DR($, X) {
    let { gen: J, data: Q, it: Y } = $;
    J.if(c3(J, Q, X, Y.opts.ownProperties), () => {
      $.setParams({ missingProperty: j$._`${X}` }, true), $.error();
    });
  }
  qw.checkReportMissingProp = DR;
  function LR({ gen: $, data: X, it: { opts: J } }, Q, Y) {
    return (0, j$.or)(...Q.map((z) => (0, j$.and)(c3($, X, z, J.ownProperties), j$._`${Y} = ${z}`)));
  }
  qw.checkMissingProp = LR;
  function jR($, X) {
    $.setParams({ missingProperty: X }, true), $.error();
  }
  qw.reportMissingProp = jR;
  function ww($) {
    return $.scopeValue("func", { ref: Object.prototype.hasOwnProperty, code: j$._`Object.prototype.hasOwnProperty` });
  }
  qw.hasPropFunc = ww;
  function l3($, X, J) {
    return j$._`${ww($)}.call(${X}, ${J})`;
  }
  qw.isOwnProperty = l3;
  function FR($, X, J, Q) {
    let Y = j$._`${X}${(0, j$.getProperty)(J)} !== undefined`;
    return Q ? j$._`${Y} && ${l3($, X, J)}` : Y;
  }
  qw.propertyInData = FR;
  function c3($, X, J, Q) {
    let Y = j$._`${X}${(0, j$.getProperty)(J)} === undefined`;
    return Q ? (0, j$.or)(Y, (0, j$.not)(l3($, X, J))) : Y;
  }
  qw.noPropertyInData = c3;
  function Bw($) {
    return $ ? Object.keys($).filter((X) => X !== "__proto__") : [];
  }
  qw.allSchemaProperties = Bw;
  function MR($, X) {
    return Bw(X).filter((J) => !(0, m3.alwaysValidSchema)($, X[J]));
  }
  qw.schemaProperties = MR;
  function IR({ schemaCode: $, data: X, it: { gen: J, topSchemaRef: Q, schemaPath: Y, errorPath: z }, it: W }, G, U, H) {
    let K = H ? j$._`${$}, ${X}, ${Q}${Y}` : X, V = [[n4.default.instancePath, (0, j$.strConcat)(n4.default.instancePath, z)], [n4.default.parentData, W.parentData], [n4.default.parentDataProperty, W.parentDataProperty], [n4.default.rootData, n4.default.rootData]];
    if (W.opts.dynamicRef) V.push([n4.default.dynamicAnchors, n4.default.dynamicAnchors]);
    let O = j$._`${K}, ${J.object(...V)}`;
    return U !== j$.nil ? j$._`${G}.call(${U}, ${O})` : j$._`${G}(${O})`;
  }
  qw.callValidateCode = IR;
  var AR = j$._`new RegExp`;
  function bR({ gen: $, it: { opts: X } }, J) {
    let Q = X.unicodeRegExp ? "u" : "", { regExp: Y } = X.code, z = Y(J, Q);
    return $.scopeValue("pattern", { key: z.toString(), ref: z, code: j$._`${Y.code === "new RegExp" ? AR : (0, qR.useFunc)($, Y)}(${J}, ${Q})` });
  }
  qw.usePattern = bR;
  function PR($) {
    let { gen: X, data: J, keyword: Q, it: Y } = $, z = X.name("valid");
    if (Y.allErrors) {
      let G = X.let("valid", true);
      return W(() => X.assign(G, false)), G;
    }
    return X.var(z, true), W(() => X.break()), z;
    function W(G) {
      let U = X.const("len", j$._`${J}.length`);
      X.forRange("i", 0, U, (H) => {
        $.subschema({ keyword: Q, dataProp: H, dataPropType: m3.Type.Num }, z), X.if((0, j$.not)(z), G);
      });
    }
  }
  qw.validateArray = PR;
  function ZR($) {
    let { gen: X, schema: J, keyword: Q, it: Y } = $;
    if (!Array.isArray(J)) throw Error("ajv implementation error");
    if (J.some((U) => (0, m3.alwaysValidSchema)(Y, U)) && !Y.opts.unevaluated) return;
    let W = X.let("valid", false), G = X.name("_valid");
    X.block(() => J.forEach((U, H) => {
      let K = $.subschema({ keyword: Q, schemaProp: H, compositeRule: true }, G);
      if (X.assign(W, j$._`${W} || ${G}`), !$.mergeValidEvaluated(K, G)) X.if((0, j$.not)(W));
    })), $.result(W, () => $.reset(), () => $.error(true));
  }
  qw.validateUnion = ZR;
});
var Iw = k((Fw) => {
  Object.defineProperty(Fw, "__esModule", { value: true });
  Fw.validateKeywordUsage = Fw.validSchemaType = Fw.funcKeywordCode = Fw.macroKeywordCode = void 0;
  var t$ = a(), S1 = B4(), hR = Z6(), uR = P9();
  function mR($, X) {
    let { gen: J, keyword: Q, schema: Y, parentSchema: z, it: W } = $, G = X.macro.call(W.self, Y, z, W), U = jw(J, Q, G);
    if (W.opts.validateSchema !== false) W.self.validateSchema(G, true);
    let H = J.name("valid");
    $.subschema({ schema: G, schemaPath: t$.nil, errSchemaPath: `${W.errSchemaPath}/${Q}`, topSchemaRef: U, compositeRule: true }, H), $.pass(H, () => $.error(true));
  }
  Fw.macroKeywordCode = mR;
  function lR($, X) {
    var J;
    let { gen: Q, keyword: Y, schema: z, parentSchema: W, $data: G, it: U } = $;
    pR(U, X);
    let H = !G && X.compile ? X.compile.call(U.self, z, W, U) : X.validate, K = jw(Q, Y, H), V = Q.let("valid");
    $.block$data(V, O), $.ok((J = X.valid) !== null && J !== void 0 ? J : V);
    function O() {
      if (X.errors === false) {
        if (B(), X.modifying) Lw($);
        L(() => $.error());
      } else {
        let j = X.async ? N() : w();
        if (X.modifying) Lw($);
        L(() => cR($, j));
      }
    }
    function N() {
      let j = Q.let("ruleErrs", null);
      return Q.try(() => B(t$._`await `), (I) => Q.assign(V, false).if(t$._`${I} instanceof ${U.ValidationError}`, () => Q.assign(j, t$._`${I}.errors`), () => Q.throw(I))), j;
    }
    function w() {
      let j = t$._`${K}.errors`;
      return Q.assign(j, null), B(t$.nil), j;
    }
    function B(j = X.async ? t$._`await ` : t$.nil) {
      let I = U.opts.passContext ? S1.default.this : S1.default.self, b = !("compile" in X && !G || X.schema === false);
      Q.assign(V, t$._`${j}${(0, hR.callValidateCode)($, K, I, b)}`, X.modifying);
    }
    function L(j) {
      var I;
      Q.if((0, t$.not)((I = X.valid) !== null && I !== void 0 ? I : V), j);
    }
  }
  Fw.funcKeywordCode = lR;
  function Lw($) {
    let { gen: X, data: J, it: Q } = $;
    X.if(Q.parentData, () => X.assign(J, t$._`${Q.parentData}[${Q.parentDataProperty}]`));
  }
  function cR($, X) {
    let { gen: J } = $;
    J.if(t$._`Array.isArray(${X})`, () => {
      J.assign(S1.default.vErrors, t$._`${S1.default.vErrors} === null ? ${X} : ${S1.default.vErrors}.concat(${X})`).assign(S1.default.errors, t$._`${S1.default.vErrors}.length`), (0, uR.extendErrors)($);
    }, () => $.error());
  }
  function pR({ schemaEnv: $ }, X) {
    if (X.async && !$.$async) throw Error("async keyword in sync schema");
  }
  function jw($, X, J) {
    if (J === void 0) throw Error(`keyword "${X}" failed to compile`);
    return $.scopeValue("keyword", typeof J == "function" ? { ref: J } : { ref: J, code: (0, t$.stringify)(J) });
  }
  function iR($, X, J = false) {
    return !X.length || X.some((Q) => Q === "array" ? Array.isArray($) : Q === "object" ? $ && typeof $ == "object" && !Array.isArray($) : typeof $ == Q || J && typeof $ > "u");
  }
  Fw.validSchemaType = iR;
  function nR({ schema: $, opts: X, self: J, errSchemaPath: Q }, Y, z) {
    if (Array.isArray(Y.keyword) ? !Y.keyword.includes(z) : Y.keyword !== z) throw Error("ajv implementation error");
    let W = Y.dependencies;
    if (W === null || W === void 0 ? void 0 : W.some((G) => !Object.prototype.hasOwnProperty.call($, G))) throw Error(`parent schema must have dependencies of ${z}: ${W.join(",")}`);
    if (Y.validateSchema) {
      if (!Y.validateSchema($[z])) {
        let U = `keyword "${z}" value is invalid at path "${Q}": ` + J.errorsText(Y.validateSchema.errors);
        if (X.validateSchema === "log") J.logger.error(U);
        else throw Error(U);
      }
    }
  }
  Fw.validateKeywordUsage = nR;
});
var Zw = k((bw) => {
  Object.defineProperty(bw, "__esModule", { value: true });
  bw.extendSubschemaMode = bw.extendSubschemaData = bw.getSubschema = void 0;
  var i6 = a(), Aw = Q$();
  function tR($, { keyword: X, schemaProp: J, schema: Q, schemaPath: Y, errSchemaPath: z, topSchemaRef: W }) {
    if (X !== void 0 && Q !== void 0) throw Error('both "keyword" and "schema" passed, only one allowed');
    if (X !== void 0) {
      let G = $.schema[X];
      return J === void 0 ? { schema: G, schemaPath: i6._`${$.schemaPath}${(0, i6.getProperty)(X)}`, errSchemaPath: `${$.errSchemaPath}/${X}` } : { schema: G[J], schemaPath: i6._`${$.schemaPath}${(0, i6.getProperty)(X)}${(0, i6.getProperty)(J)}`, errSchemaPath: `${$.errSchemaPath}/${X}/${(0, Aw.escapeFragment)(J)}` };
    }
    if (Q !== void 0) {
      if (Y === void 0 || z === void 0 || W === void 0) throw Error('"schemaPath", "errSchemaPath" and "topSchemaRef" are required with "schema"');
      return { schema: Q, schemaPath: Y, topSchemaRef: W, errSchemaPath: z };
    }
    throw Error('either "keyword" or "schema" must be passed');
  }
  bw.getSubschema = tR;
  function aR($, X, { dataProp: J, dataPropType: Q, data: Y, dataTypes: z, propertyName: W }) {
    if (Y !== void 0 && J !== void 0) throw Error('both "data" and "dataProp" passed, only one allowed');
    let { gen: G } = X;
    if (J !== void 0) {
      let { errorPath: H, dataPathArr: K, opts: V } = X, O = G.let("data", i6._`${X.data}${(0, i6.getProperty)(J)}`, true);
      U(O), $.errorPath = i6.str`${H}${(0, Aw.getErrorPath)(J, Q, V.jsPropertySyntax)}`, $.parentDataProperty = i6._`${J}`, $.dataPathArr = [...K, $.parentDataProperty];
    }
    if (Y !== void 0) {
      let H = Y instanceof i6.Name ? Y : G.let("data", Y, true);
      if (U(H), W !== void 0) $.propertyName = W;
    }
    if (z) $.dataTypes = z;
    function U(H) {
      $.data = H, $.dataLevel = X.dataLevel + 1, $.dataTypes = [], X.definedProperties = /* @__PURE__ */ new Set(), $.parentData = X.data, $.dataNames = [...X.dataNames, H];
    }
  }
  bw.extendSubschemaData = aR;
  function sR($, { jtdDiscriminator: X, jtdMetadata: J, compositeRule: Q, createErrors: Y, allErrors: z }) {
    if (Q !== void 0) $.compositeRule = Q;
    if (Y !== void 0) $.createErrors = Y;
    if (z !== void 0) $.allErrors = z;
    $.jtdDiscriminator = X, $.jtdMetadata = J;
  }
  bw.extendSubschemaMode = sR;
});
var p3 = k((co, Ew) => {
  Ew.exports = function $(X, J) {
    if (X === J) return true;
    if (X && J && typeof X == "object" && typeof J == "object") {
      if (X.constructor !== J.constructor) return false;
      var Q, Y, z;
      if (Array.isArray(X)) {
        if (Q = X.length, Q != J.length) return false;
        for (Y = Q; Y-- !== 0; ) if (!$(X[Y], J[Y])) return false;
        return true;
      }
      if (X.constructor === RegExp) return X.source === J.source && X.flags === J.flags;
      if (X.valueOf !== Object.prototype.valueOf) return X.valueOf() === J.valueOf();
      if (X.toString !== Object.prototype.toString) return X.toString() === J.toString();
      if (z = Object.keys(X), Q = z.length, Q !== Object.keys(J).length) return false;
      for (Y = Q; Y-- !== 0; ) if (!Object.prototype.hasOwnProperty.call(J, z[Y])) return false;
      for (Y = Q; Y-- !== 0; ) {
        var W = z[Y];
        if (!$(X[W], J[W])) return false;
      }
      return true;
    }
    return X !== X && J !== J;
  };
});
var Sw = k((po, Rw) => {
  var d4 = Rw.exports = function($, X, J) {
    if (typeof X == "function") J = X, X = {};
    J = X.cb || J;
    var Q = typeof J == "function" ? J : J.pre || function() {
    }, Y = J.post || function() {
    };
    EQ(X, Q, Y, $, "", $);
  };
  d4.keywords = { additionalItems: true, items: true, contains: true, additionalProperties: true, propertyNames: true, not: true, if: true, then: true, else: true };
  d4.arrayKeywords = { items: true, allOf: true, anyOf: true, oneOf: true };
  d4.propsKeywords = { $defs: true, definitions: true, properties: true, patternProperties: true, dependencies: true };
  d4.skipKeywords = { default: true, enum: true, const: true, required: true, maximum: true, minimum: true, exclusiveMaximum: true, exclusiveMinimum: true, multipleOf: true, maxLength: true, minLength: true, pattern: true, format: true, maxItems: true, minItems: true, uniqueItems: true, maxProperties: true, minProperties: true };
  function EQ($, X, J, Q, Y, z, W, G, U, H) {
    if (Q && typeof Q == "object" && !Array.isArray(Q)) {
      X(Q, Y, z, W, G, U, H);
      for (var K in Q) {
        var V = Q[K];
        if (Array.isArray(V)) {
          if (K in d4.arrayKeywords) for (var O = 0; O < V.length; O++) EQ($, X, J, V[O], Y + "/" + K + "/" + O, z, Y, K, Q, O);
        } else if (K in d4.propsKeywords) {
          if (V && typeof V == "object") for (var N in V) EQ($, X, J, V[N], Y + "/" + K + "/" + XS(N), z, Y, K, Q, N);
        } else if (K in d4.keywords || $.allKeys && !(K in d4.skipKeywords)) EQ($, X, J, V, Y + "/" + K, z, Y, K, Q);
      }
      J(Q, Y, z, W, G, U, H);
    }
  }
  function XS($) {
    return $.replace(/~/g, "~0").replace(/\//g, "~1");
  }
});
var E9 = k((_w) => {
  Object.defineProperty(_w, "__esModule", { value: true });
  _w.getSchemaRefs = _w.resolveUrl = _w.normalizeId = _w._getFullPath = _w.getFullPath = _w.inlineRef = void 0;
  var JS = Q$(), YS = p3(), QS = Sw(), zS = /* @__PURE__ */ new Set(["type", "format", "pattern", "maxLength", "minLength", "maxProperties", "minProperties", "maxItems", "minItems", "maximum", "minimum", "uniqueItems", "multipleOf", "required", "enum", "const"]);
  function WS($, X = true) {
    if (typeof $ == "boolean") return true;
    if (X === true) return !i3($);
    if (!X) return false;
    return vw($) <= X;
  }
  _w.inlineRef = WS;
  var GS = /* @__PURE__ */ new Set(["$ref", "$recursiveRef", "$recursiveAnchor", "$dynamicRef", "$dynamicAnchor"]);
  function i3($) {
    for (let X in $) {
      if (GS.has(X)) return true;
      let J = $[X];
      if (Array.isArray(J) && J.some(i3)) return true;
      if (typeof J == "object" && i3(J)) return true;
    }
    return false;
  }
  function vw($) {
    let X = 0;
    for (let J in $) {
      if (J === "$ref") return 1 / 0;
      if (X++, zS.has(J)) continue;
      if (typeof $[J] == "object") (0, JS.eachItem)($[J], (Q) => X += vw(Q));
      if (X === 1 / 0) return 1 / 0;
    }
    return X;
  }
  function Cw($, X = "", J) {
    if (J !== false) X = m0(X);
    let Q = $.parse(X);
    return kw($, Q);
  }
  _w.getFullPath = Cw;
  function kw($, X) {
    return $.serialize(X).split("#")[0] + "#";
  }
  _w._getFullPath = kw;
  var US = /#\/?$/;
  function m0($) {
    return $ ? $.replace(US, "") : "";
  }
  _w.normalizeId = m0;
  function HS($, X, J) {
    return J = m0(J), $.resolve(X, J);
  }
  _w.resolveUrl = HS;
  var KS = /^[a-z_][-a-z0-9._]*$/i;
  function NS($, X) {
    if (typeof $ == "boolean") return {};
    let { schemaId: J, uriResolver: Q } = this.opts, Y = m0($[J] || X), z = { "": Y }, W = Cw(Q, Y, false), G = {}, U = /* @__PURE__ */ new Set();
    return QS($, { allKeys: true }, (V, O, N, w) => {
      if (w === void 0) return;
      let B = W + O, L = z[w];
      if (typeof V[J] == "string") L = j.call(this, V[J]);
      I.call(this, V.$anchor), I.call(this, V.$dynamicAnchor), z[O] = L;
      function j(b) {
        let x = this.opts.uriResolver.resolve;
        if (b = m0(L ? x(L, b) : b), U.has(b)) throw K(b);
        U.add(b);
        let h = this.refs[b];
        if (typeof h == "string") h = this.refs[h];
        if (typeof h == "object") H(V, h.schema, b);
        else if (b !== m0(B)) if (b[0] === "#") H(V, G[b], b), G[b] = V;
        else this.refs[b] = B;
        return b;
      }
      function I(b) {
        if (typeof b == "string") {
          if (!KS.test(b)) throw Error(`invalid anchor "${b}"`);
          j.call(this, `#${b}`);
        }
      }
    }), G;
    function H(V, O, N) {
      if (O !== void 0 && !YS(V, O)) throw K(N);
    }
    function K(V) {
      return Error(`reference "${V}" resolves to more than one schema`);
    }
  }
  _w.getSchemaRefs = NS;
});
var v9 = k((ow) => {
  Object.defineProperty(ow, "__esModule", { value: true });
  ow.getData = ow.KeywordCxt = ow.validateFunctionCode = void 0;
  var hw = sO(), Tw = Z9(), d3 = f3(), RQ = Z9(), DS = Ow(), S9 = Iw(), n3 = Zw(), u = a(), d = B4(), LS = E9(), q4 = Q$(), R9 = P9();
  function jS($) {
    if (lw($)) {
      if (cw($), mw($)) {
        IS($);
        return;
      }
    }
    uw($, () => (0, hw.topBoolOrEmptySchema)($));
  }
  ow.validateFunctionCode = jS;
  function uw({ gen: $, validateName: X, schema: J, schemaEnv: Q, opts: Y }, z) {
    if (Y.code.es5) $.func(X, u._`${d.default.data}, ${d.default.valCxt}`, Q.$async, () => {
      $.code(u._`"use strict"; ${yw(J, Y)}`), MS($, Y), $.code(z);
    });
    else $.func(X, u._`${d.default.data}, ${FS(Y)}`, Q.$async, () => $.code(yw(J, Y)).code(z));
  }
  function FS($) {
    return u._`{${d.default.instancePath}="", ${d.default.parentData}, ${d.default.parentDataProperty}, ${d.default.rootData}=${d.default.data}${$.dynamicRef ? u._`, ${d.default.dynamicAnchors}={}` : u.nil}}={}`;
  }
  function MS($, X) {
    $.if(d.default.valCxt, () => {
      if ($.var(d.default.instancePath, u._`${d.default.valCxt}.${d.default.instancePath}`), $.var(d.default.parentData, u._`${d.default.valCxt}.${d.default.parentData}`), $.var(d.default.parentDataProperty, u._`${d.default.valCxt}.${d.default.parentDataProperty}`), $.var(d.default.rootData, u._`${d.default.valCxt}.${d.default.rootData}`), X.dynamicRef) $.var(d.default.dynamicAnchors, u._`${d.default.valCxt}.${d.default.dynamicAnchors}`);
    }, () => {
      if ($.var(d.default.instancePath, u._`""`), $.var(d.default.parentData, u._`undefined`), $.var(d.default.parentDataProperty, u._`undefined`), $.var(d.default.rootData, d.default.data), X.dynamicRef) $.var(d.default.dynamicAnchors, u._`{}`);
    });
  }
  function IS($) {
    let { schema: X, opts: J, gen: Q } = $;
    uw($, () => {
      if (J.$comment && X.$comment) iw($);
      if (ES($), Q.let(d.default.vErrors, null), Q.let(d.default.errors, 0), J.unevaluated) AS($);
      pw($), vS($);
    });
    return;
  }
  function AS($) {
    let { gen: X, validateName: J } = $;
    $.evaluated = X.const("evaluated", u._`${J}.evaluated`), X.if(u._`${$.evaluated}.dynamicProps`, () => X.assign(u._`${$.evaluated}.props`, u._`undefined`)), X.if(u._`${$.evaluated}.dynamicItems`, () => X.assign(u._`${$.evaluated}.items`, u._`undefined`));
  }
  function yw($, X) {
    let J = typeof $ == "object" && $[X.schemaId];
    return J && (X.code.source || X.code.process) ? u._`/*# sourceURL=${J} */` : u.nil;
  }
  function bS($, X) {
    if (lw($)) {
      if (cw($), mw($)) {
        PS($, X);
        return;
      }
    }
    (0, hw.boolOrEmptySchema)($, X);
  }
  function mw({ schema: $, self: X }) {
    if (typeof $ == "boolean") return !$;
    for (let J in $) if (X.RULES.all[J]) return true;
    return false;
  }
  function lw($) {
    return typeof $.schema != "boolean";
  }
  function PS($, X) {
    let { schema: J, gen: Q, opts: Y } = $;
    if (Y.$comment && J.$comment) iw($);
    RS($), SS($);
    let z = Q.const("_errs", d.default.errors);
    pw($, z), Q.var(X, u._`${z} === ${d.default.errors}`);
  }
  function cw($) {
    (0, q4.checkUnknownRules)($), ZS($);
  }
  function pw($, X) {
    if ($.opts.jtd) return fw($, [], false, X);
    let J = (0, Tw.getSchemaTypes)($.schema), Q = (0, Tw.coerceAndCheckDataType)($, J);
    fw($, J, !Q, X);
  }
  function ZS($) {
    let { schema: X, errSchemaPath: J, opts: Q, self: Y } = $;
    if (X.$ref && Q.ignoreKeywordsWithRef && (0, q4.schemaHasRulesButRef)(X, Y.RULES)) Y.logger.warn(`$ref: keywords ignored in schema at path "${J}"`);
  }
  function ES($) {
    let { schema: X, opts: J } = $;
    if (X.default !== void 0 && J.useDefaults && J.strictSchema) (0, q4.checkStrictMode)($, "default is ignored in the schema root");
  }
  function RS($) {
    let X = $.schema[$.opts.schemaId];
    if (X) $.baseId = (0, LS.resolveUrl)($.opts.uriResolver, $.baseId, X);
  }
  function SS($) {
    if ($.schema.$async && !$.schemaEnv.$async) throw Error("async schema in sync schema");
  }
  function iw({ gen: $, schemaEnv: X, schema: J, errSchemaPath: Q, opts: Y }) {
    let z = J.$comment;
    if (Y.$comment === true) $.code(u._`${d.default.self}.logger.log(${z})`);
    else if (typeof Y.$comment == "function") {
      let W = u.str`${Q}/$comment`, G = $.scopeValue("root", { ref: X.root });
      $.code(u._`${d.default.self}.opts.$comment(${z}, ${W}, ${G}.schema)`);
    }
  }
  function vS($) {
    let { gen: X, schemaEnv: J, validateName: Q, ValidationError: Y, opts: z } = $;
    if (J.$async) X.if(u._`${d.default.errors} === 0`, () => X.return(d.default.data), () => X.throw(u._`new ${Y}(${d.default.vErrors})`));
    else {
      if (X.assign(u._`${Q}.errors`, d.default.vErrors), z.unevaluated) CS($);
      X.return(u._`${d.default.errors} === 0`);
    }
  }
  function CS({ gen: $, evaluated: X, props: J, items: Q }) {
    if (J instanceof u.Name) $.assign(u._`${X}.props`, J);
    if (Q instanceof u.Name) $.assign(u._`${X}.items`, Q);
  }
  function fw($, X, J, Q) {
    let { gen: Y, schema: z, data: W, allErrors: G, opts: U, self: H } = $, { RULES: K } = H;
    if (z.$ref && (U.ignoreKeywordsWithRef || !(0, q4.schemaHasRulesButRef)(z, K))) {
      Y.block(() => dw($, "$ref", K.all.$ref.definition));
      return;
    }
    if (!U.jtd) kS($, X);
    Y.block(() => {
      for (let O of K.rules) V(O);
      V(K.post);
    });
    function V(O) {
      if (!(0, d3.shouldUseGroup)(z, O)) return;
      if (O.type) {
        if (Y.if((0, RQ.checkDataType)(O.type, W, U.strictNumbers)), gw($, O), X.length === 1 && X[0] === O.type && J) Y.else(), (0, RQ.reportTypeError)($);
        Y.endIf();
      } else gw($, O);
      if (!G) Y.if(u._`${d.default.errors} === ${Q || 0}`);
    }
  }
  function gw($, X) {
    let { gen: J, schema: Q, opts: { useDefaults: Y } } = $;
    if (Y) (0, DS.assignDefaults)($, X.type);
    J.block(() => {
      for (let z of X.rules) if ((0, d3.shouldUseRule)(Q, z)) dw($, z.keyword, z.definition, X.type);
    });
  }
  function kS($, X) {
    if ($.schemaEnv.meta || !$.opts.strictTypes) return;
    if (_S($, X), !$.opts.allowUnionTypes) xS($, X);
    TS($, $.dataTypes);
  }
  function _S($, X) {
    if (!X.length) return;
    if (!$.dataTypes.length) {
      $.dataTypes = X;
      return;
    }
    X.forEach((J) => {
      if (!nw($.dataTypes, J)) r3($, `type "${J}" not allowed by context "${$.dataTypes.join(",")}"`);
    }), fS($, X);
  }
  function xS($, X) {
    if (X.length > 1 && !(X.length === 2 && X.includes("null"))) r3($, "use allowUnionTypes to allow union type keyword");
  }
  function TS($, X) {
    let J = $.self.RULES.all;
    for (let Q in J) {
      let Y = J[Q];
      if (typeof Y == "object" && (0, d3.shouldUseRule)($.schema, Y)) {
        let { type: z } = Y.definition;
        if (z.length && !z.some((W) => yS(X, W))) r3($, `missing type "${z.join(",")}" for keyword "${Q}"`);
      }
    }
  }
  function yS($, X) {
    return $.includes(X) || X === "number" && $.includes("integer");
  }
  function nw($, X) {
    return $.includes(X) || X === "integer" && $.includes("number");
  }
  function fS($, X) {
    let J = [];
    for (let Q of $.dataTypes) if (nw(X, Q)) J.push(Q);
    else if (X.includes("integer") && Q === "number") J.push("integer");
    $.dataTypes = J;
  }
  function r3($, X) {
    let J = $.schemaEnv.baseId + $.errSchemaPath;
    X += ` at "${J}" (strictTypes)`, (0, q4.checkStrictMode)($, X, $.opts.strictTypes);
  }
  class o3 {
    constructor($, X, J) {
      if ((0, S9.validateKeywordUsage)($, X, J), this.gen = $.gen, this.allErrors = $.allErrors, this.keyword = J, this.data = $.data, this.schema = $.schema[J], this.$data = X.$data && $.opts.$data && this.schema && this.schema.$data, this.schemaValue = (0, q4.schemaRefOrVal)($, this.schema, J, this.$data), this.schemaType = X.schemaType, this.parentSchema = $.schema, this.params = {}, this.it = $, this.def = X, this.$data) this.schemaCode = $.gen.const("vSchema", rw(this.$data, $));
      else if (this.schemaCode = this.schemaValue, !(0, S9.validSchemaType)(this.schema, X.schemaType, X.allowUndefined)) throw Error(`${J} value must be ${JSON.stringify(X.schemaType)}`);
      if ("code" in X ? X.trackErrors : X.errors !== false) this.errsCount = $.gen.const("_errs", d.default.errors);
    }
    result($, X, J) {
      this.failResult((0, u.not)($), X, J);
    }
    failResult($, X, J) {
      if (this.gen.if($), J) J();
      else this.error();
      if (X) {
        if (this.gen.else(), X(), this.allErrors) this.gen.endIf();
      } else if (this.allErrors) this.gen.endIf();
      else this.gen.else();
    }
    pass($, X) {
      this.failResult((0, u.not)($), void 0, X);
    }
    fail($) {
      if ($ === void 0) {
        if (this.error(), !this.allErrors) this.gen.if(false);
        return;
      }
      if (this.gen.if($), this.error(), this.allErrors) this.gen.endIf();
      else this.gen.else();
    }
    fail$data($) {
      if (!this.$data) return this.fail($);
      let { schemaCode: X } = this;
      this.fail(u._`${X} !== undefined && (${(0, u.or)(this.invalid$data(), $)})`);
    }
    error($, X, J) {
      if (X) {
        this.setParams(X), this._error($, J), this.setParams({});
        return;
      }
      this._error($, J);
    }
    _error($, X) {
      ($ ? R9.reportExtraError : R9.reportError)(this, this.def.error, X);
    }
    $dataError() {
      (0, R9.reportError)(this, this.def.$dataError || R9.keyword$DataError);
    }
    reset() {
      if (this.errsCount === void 0) throw Error('add "trackErrors" to keyword definition');
      (0, R9.resetErrorsCount)(this.gen, this.errsCount);
    }
    ok($) {
      if (!this.allErrors) this.gen.if($);
    }
    setParams($, X) {
      if (X) Object.assign(this.params, $);
      else this.params = $;
    }
    block$data($, X, J = u.nil) {
      this.gen.block(() => {
        this.check$data($, J), X();
      });
    }
    check$data($ = u.nil, X = u.nil) {
      if (!this.$data) return;
      let { gen: J, schemaCode: Q, schemaType: Y, def: z } = this;
      if (J.if((0, u.or)(u._`${Q} === undefined`, X)), $ !== u.nil) J.assign($, true);
      if (Y.length || z.validateSchema) {
        if (J.elseIf(this.invalid$data()), this.$dataError(), $ !== u.nil) J.assign($, false);
      }
      J.else();
    }
    invalid$data() {
      let { gen: $, schemaCode: X, schemaType: J, def: Q, it: Y } = this;
      return (0, u.or)(z(), W());
      function z() {
        if (J.length) {
          if (!(X instanceof u.Name)) throw Error("ajv implementation error");
          let G = Array.isArray(J) ? J : [J];
          return u._`${(0, RQ.checkDataTypes)(G, X, Y.opts.strictNumbers, RQ.DataType.Wrong)}`;
        }
        return u.nil;
      }
      function W() {
        if (Q.validateSchema) {
          let G = $.scopeValue("validate$data", { ref: Q.validateSchema });
          return u._`!${G}(${X})`;
        }
        return u.nil;
      }
    }
    subschema($, X) {
      let J = (0, n3.getSubschema)(this.it, $);
      (0, n3.extendSubschemaData)(J, this.it, $), (0, n3.extendSubschemaMode)(J, $);
      let Q = { ...this.it, ...J, items: void 0, props: void 0 };
      return bS(Q, X), Q;
    }
    mergeEvaluated($, X) {
      let { it: J, gen: Q } = this;
      if (!J.opts.unevaluated) return;
      if (J.props !== true && $.props !== void 0) J.props = q4.mergeEvaluated.props(Q, $.props, J.props, X);
      if (J.items !== true && $.items !== void 0) J.items = q4.mergeEvaluated.items(Q, $.items, J.items, X);
    }
    mergeValidEvaluated($, X) {
      let { it: J, gen: Q } = this;
      if (J.opts.unevaluated && (J.props !== true || J.items !== true)) return Q.if(X, () => this.mergeEvaluated($, u.Name)), true;
    }
  }
  ow.KeywordCxt = o3;
  function dw($, X, J, Q) {
    let Y = new o3($, J, X);
    if ("code" in J) J.code(Y, Q);
    else if (Y.$data && J.validate) (0, S9.funcKeywordCode)(Y, J);
    else if ("macro" in J) (0, S9.macroKeywordCode)(Y, J);
    else if (J.compile || J.validate) (0, S9.funcKeywordCode)(Y, J);
  }
  var gS = /^\/(?:[^~]|~0|~1)*$/, hS = /^([0-9]+)(#|\/(?:[^~]|~0|~1)*)?$/;
  function rw($, { dataLevel: X, dataNames: J, dataPathArr: Q }) {
    let Y, z;
    if ($ === "") return d.default.rootData;
    if ($[0] === "/") {
      if (!gS.test($)) throw Error(`Invalid JSON-pointer: ${$}`);
      Y = $, z = d.default.rootData;
    } else {
      let H = hS.exec($);
      if (!H) throw Error(`Invalid JSON-pointer: ${$}`);
      let K = +H[1];
      if (Y = H[2], Y === "#") {
        if (K >= X) throw Error(U("property/index", K));
        return Q[X - K];
      }
      if (K > X) throw Error(U("data", K));
      if (z = J[X - K], !Y) return z;
    }
    let W = z, G = Y.split("/");
    for (let H of G) if (H) z = u._`${z}${(0, u.getProperty)((0, q4.unescapeJsonPointer)(H))}`, W = u._`${W} && ${z}`;
    return W;
    function U(H, K) {
      return `Cannot access ${H} ${K} levels up, current level is ${X}`;
    }
  }
  ow.getData = rw;
});
var SQ = k((sw) => {
  Object.defineProperty(sw, "__esModule", { value: true });
  class aw extends Error {
    constructor($) {
      super("validation failed");
      this.errors = $, this.ajv = this.validation = true;
    }
  }
  sw.default = aw;
});
var C9 = k(($B) => {
  Object.defineProperty($B, "__esModule", { value: true });
  var t3 = E9();
  class ew extends Error {
    constructor($, X, J, Q) {
      super(Q || `can't resolve reference ${J} from id ${X}`);
      this.missingRef = (0, t3.resolveUrl)($, X, J), this.missingSchema = (0, t3.normalizeId)((0, t3.getFullPath)($, this.missingRef));
    }
  }
  $B.default = ew;
});
var CQ = k((YB) => {
  Object.defineProperty(YB, "__esModule", { value: true });
  YB.resolveSchema = YB.getCompilingSchema = YB.resolveRef = YB.compileSchema = YB.SchemaEnv = void 0;
  var y6 = a(), pS = SQ(), v1 = B4(), f6 = E9(), XB = Q$(), iS = v9();
  class k9 {
    constructor($) {
      var X;
      this.refs = {}, this.dynamicAnchors = {};
      let J;
      if (typeof $.schema == "object") J = $.schema;
      this.schema = $.schema, this.schemaId = $.schemaId, this.root = $.root || this, this.baseId = (X = $.baseId) !== null && X !== void 0 ? X : (0, f6.normalizeId)(J === null || J === void 0 ? void 0 : J[$.schemaId || "$id"]), this.schemaPath = $.schemaPath, this.localRefs = $.localRefs, this.meta = $.meta, this.$async = J === null || J === void 0 ? void 0 : J.$async, this.refs = {};
    }
  }
  YB.SchemaEnv = k9;
  function s3($) {
    let X = JB.call(this, $);
    if (X) return X;
    let J = (0, f6.getFullPath)(this.opts.uriResolver, $.root.baseId), { es5: Q, lines: Y } = this.opts.code, { ownProperties: z } = this.opts, W = new y6.CodeGen(this.scope, { es5: Q, lines: Y, ownProperties: z }), G;
    if ($.$async) G = W.scopeValue("Error", { ref: pS.default, code: y6._`require("ajv/dist/runtime/validation_error").default` });
    let U = W.scopeName("validate");
    $.validateName = U;
    let H = { gen: W, allErrors: this.opts.allErrors, data: v1.default.data, parentData: v1.default.parentData, parentDataProperty: v1.default.parentDataProperty, dataNames: [v1.default.data], dataPathArr: [y6.nil], dataLevel: 0, dataTypes: [], definedProperties: /* @__PURE__ */ new Set(), topSchemaRef: W.scopeValue("schema", this.opts.code.source === true ? { ref: $.schema, code: (0, y6.stringify)($.schema) } : { ref: $.schema }), validateName: U, ValidationError: G, schema: $.schema, schemaEnv: $, rootId: J, baseId: $.baseId || J, schemaPath: y6.nil, errSchemaPath: $.schemaPath || (this.opts.jtd ? "" : "#"), errorPath: y6._`""`, opts: this.opts, self: this }, K;
    try {
      this._compilations.add($), (0, iS.validateFunctionCode)(H), W.optimize(this.opts.code.optimize);
      let V = W.toString();
      if (K = `${W.scopeRefs(v1.default.scope)}return ${V}`, this.opts.code.process) K = this.opts.code.process(K, $);
      let N = Function(`${v1.default.self}`, `${v1.default.scope}`, K)(this, this.scope.get());
      if (this.scope.value(U, { ref: N }), N.errors = null, N.schema = $.schema, N.schemaEnv = $, $.$async) N.$async = true;
      if (this.opts.code.source === true) N.source = { validateName: U, validateCode: V, scopeValues: W._values };
      if (this.opts.unevaluated) {
        let { props: w, items: B } = H;
        if (N.evaluated = { props: w instanceof y6.Name ? void 0 : w, items: B instanceof y6.Name ? void 0 : B, dynamicProps: w instanceof y6.Name, dynamicItems: B instanceof y6.Name }, N.source) N.source.evaluated = (0, y6.stringify)(N.evaluated);
      }
      return $.validate = N, $;
    } catch (V) {
      if (delete $.validate, delete $.validateName, K) this.logger.error("Error compiling schema, function code:", K);
      throw V;
    } finally {
      this._compilations.delete($);
    }
  }
  YB.compileSchema = s3;
  function nS($, X, J) {
    var Q;
    J = (0, f6.resolveUrl)(this.opts.uriResolver, X, J);
    let Y = $.refs[J];
    if (Y) return Y;
    let z = oS.call(this, $, J);
    if (z === void 0) {
      let W = (Q = $.localRefs) === null || Q === void 0 ? void 0 : Q[J], { schemaId: G } = this.opts;
      if (W) z = new k9({ schema: W, schemaId: G, root: $, baseId: X });
    }
    if (z === void 0) return;
    return $.refs[J] = dS.call(this, z);
  }
  YB.resolveRef = nS;
  function dS($) {
    if ((0, f6.inlineRef)($.schema, this.opts.inlineRefs)) return $.schema;
    return $.validate ? $ : s3.call(this, $);
  }
  function JB($) {
    for (let X of this._compilations) if (rS(X, $)) return X;
  }
  YB.getCompilingSchema = JB;
  function rS($, X) {
    return $.schema === X.schema && $.root === X.root && $.baseId === X.baseId;
  }
  function oS($, X) {
    let J;
    while (typeof (J = this.refs[X]) == "string") X = J;
    return J || this.schemas[X] || vQ.call(this, $, X);
  }
  function vQ($, X) {
    let J = this.opts.uriResolver.parse(X), Q = (0, f6._getFullPath)(this.opts.uriResolver, J), Y = (0, f6.getFullPath)(this.opts.uriResolver, $.baseId, void 0);
    if (Object.keys($.schema).length > 0 && Q === Y) return a3.call(this, J, $);
    let z = (0, f6.normalizeId)(Q), W = this.refs[z] || this.schemas[z];
    if (typeof W == "string") {
      let G = vQ.call(this, $, W);
      if (typeof (G === null || G === void 0 ? void 0 : G.schema) !== "object") return;
      return a3.call(this, J, G);
    }
    if (typeof (W === null || W === void 0 ? void 0 : W.schema) !== "object") return;
    if (!W.validate) s3.call(this, W);
    if (z === (0, f6.normalizeId)(X)) {
      let { schema: G } = W, { schemaId: U } = this.opts, H = G[U];
      if (H) Y = (0, f6.resolveUrl)(this.opts.uriResolver, Y, H);
      return new k9({ schema: G, schemaId: U, root: $, baseId: Y });
    }
    return a3.call(this, J, W);
  }
  YB.resolveSchema = vQ;
  var tS = /* @__PURE__ */ new Set(["properties", "patternProperties", "enum", "dependencies", "definitions"]);
  function a3($, { baseId: X, schema: J, root: Q }) {
    var Y;
    if (((Y = $.fragment) === null || Y === void 0 ? void 0 : Y[0]) !== "/") return;
    for (let G of $.fragment.slice(1).split("/")) {
      if (typeof J === "boolean") return;
      let U = J[(0, XB.unescapeFragment)(G)];
      if (U === void 0) return;
      J = U;
      let H = typeof J === "object" && J[this.opts.schemaId];
      if (!tS.has(G) && H) X = (0, f6.resolveUrl)(this.opts.uriResolver, X, H);
    }
    let z;
    if (typeof J != "boolean" && J.$ref && !(0, XB.schemaHasRulesButRef)(J, this.RULES)) {
      let G = (0, f6.resolveUrl)(this.opts.uriResolver, X, J.$ref);
      z = vQ.call(this, Q, G);
    }
    let { schemaId: W } = this.opts;
    if (z = z || new k9({ schema: J, schemaId: W, root: Q, baseId: X }), z.schema !== z.root.schema) return z;
    return;
  }
});
var zB = k((ao, Xv) => {
  Xv.exports = { $id: "https://raw.githubusercontent.com/ajv-validator/ajv/master/lib/refs/data.json#", description: "Meta-schema for $data reference (JSON AnySchema extension proposal)", type: "object", required: ["$data"], properties: { $data: { type: "string", anyOf: [{ format: "relative-json-pointer" }, { format: "json-pointer" }] } }, additionalProperties: false };
});
var GB = k((so, WB) => {
  var Jv = { 0: 0, 1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 6, 7: 7, 8: 8, 9: 9, a: 10, A: 10, b: 11, B: 11, c: 12, C: 12, d: 13, D: 13, e: 14, E: 14, f: 15, F: 15 };
  WB.exports = { HEX: Jv };
});
var BB = k((eo, wB) => {
  var { HEX: Yv } = GB(), Qv = /^(?:(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)$/u;
  function NB($) {
    if (OB($, ".") < 3) return { host: $, isIPV4: false };
    let X = $.match(Qv) || [], [J] = X;
    if (J) return { host: Wv(J, "."), isIPV4: true };
    else return { host: $, isIPV4: false };
  }
  function e3($, X = false) {
    let J = "", Q = true;
    for (let Y of $) {
      if (Yv[Y] === void 0) return;
      if (Y !== "0" && Q === true) Q = false;
      if (!Q) J += Y;
    }
    if (X && J.length === 0) J = "0";
    return J;
  }
  function zv($) {
    let X = 0, J = { error: false, address: "", zone: "" }, Q = [], Y = [], z = false, W = false, G = false;
    function U() {
      if (Y.length) {
        if (z === false) {
          let H = e3(Y);
          if (H !== void 0) Q.push(H);
          else return J.error = true, false;
        }
        Y.length = 0;
      }
      return true;
    }
    for (let H = 0; H < $.length; H++) {
      let K = $[H];
      if (K === "[" || K === "]") continue;
      if (K === ":") {
        if (W === true) G = true;
        if (!U()) break;
        if (X++, Q.push(":"), X > 7) {
          J.error = true;
          break;
        }
        if (H - 1 >= 0 && $[H - 1] === ":") W = true;
        continue;
      } else if (K === "%") {
        if (!U()) break;
        z = true;
      } else {
        Y.push(K);
        continue;
      }
    }
    if (Y.length) if (z) J.zone = Y.join("");
    else if (G) Q.push(Y.join(""));
    else Q.push(e3(Y));
    return J.address = Q.join(""), J;
  }
  function VB($) {
    if (OB($, ":") < 2) return { host: $, isIPV6: false };
    let X = zv($);
    if (!X.error) {
      let { address: J, address: Q } = X;
      if (X.zone) J += "%" + X.zone, Q += "%25" + X.zone;
      return { host: J, escapedHost: Q, isIPV6: true };
    } else return { host: $, isIPV6: false };
  }
  function Wv($, X) {
    let J = "", Q = true, Y = $.length;
    for (let z = 0; z < Y; z++) {
      let W = $[z];
      if (W === "0" && Q) {
        if (z + 1 <= Y && $[z + 1] === X || z + 1 === Y) J += W, Q = false;
      } else {
        if (W === X) Q = true;
        else Q = false;
        J += W;
      }
    }
    return J;
  }
  function OB($, X) {
    let J = 0;
    for (let Q = 0; Q < $.length; Q++) if ($[Q] === X) J++;
    return J;
  }
  var UB = /^\.\.?\//u, HB = /^\/\.(?:\/|$)/u, KB = /^\/\.\.(?:\/|$)/u, Gv = /^\/?(?:.|\n)*?(?=\/|$)/u;
  function Uv($) {
    let X = [];
    while ($.length) if ($.match(UB)) $ = $.replace(UB, "");
    else if ($.match(HB)) $ = $.replace(HB, "/");
    else if ($.match(KB)) $ = $.replace(KB, "/"), X.pop();
    else if ($ === "." || $ === "..") $ = "";
    else {
      let J = $.match(Gv);
      if (J) {
        let Q = J[0];
        $ = $.slice(Q.length), X.push(Q);
      } else throw Error("Unexpected dot segment condition");
    }
    return X.join("");
  }
  function Hv($, X) {
    let J = X !== true ? escape : unescape;
    if ($.scheme !== void 0) $.scheme = J($.scheme);
    if ($.userinfo !== void 0) $.userinfo = J($.userinfo);
    if ($.host !== void 0) $.host = J($.host);
    if ($.path !== void 0) $.path = J($.path);
    if ($.query !== void 0) $.query = J($.query);
    if ($.fragment !== void 0) $.fragment = J($.fragment);
    return $;
  }
  function Kv($) {
    let X = [];
    if ($.userinfo !== void 0) X.push($.userinfo), X.push("@");
    if ($.host !== void 0) {
      let J = unescape($.host), Q = NB(J);
      if (Q.isIPV4) J = Q.host;
      else {
        let Y = VB(Q.host);
        if (Y.isIPV6 === true) J = `[${Y.escapedHost}]`;
        else J = $.host;
      }
      X.push(J);
    }
    if (typeof $.port === "number" || typeof $.port === "string") X.push(":"), X.push(String($.port));
    return X.length ? X.join("") : void 0;
  }
  wB.exports = { recomposeAuthority: Kv, normalizeComponentEncoding: Hv, removeDotSegments: Uv, normalizeIPv4: NB, normalizeIPv6: VB, stringArrayToHexStripped: e3 };
});
var MB = k(($t, FB) => {
  var Nv = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/iu, Vv = /([\da-z][\d\-a-z]{0,31}):((?:[\w!$'()*+,\-.:;=@]|%[\da-f]{2})+)/iu;
  function qB($) {
    return typeof $.secure === "boolean" ? $.secure : String($.scheme).toLowerCase() === "wss";
  }
  function DB($) {
    if (!$.host) $.error = $.error || "HTTP URIs must have a host.";
    return $;
  }
  function LB($) {
    let X = String($.scheme).toLowerCase() === "https";
    if ($.port === (X ? 443 : 80) || $.port === "") $.port = void 0;
    if (!$.path) $.path = "/";
    return $;
  }
  function Ov($) {
    return $.secure = qB($), $.resourceName = ($.path || "/") + ($.query ? "?" + $.query : ""), $.path = void 0, $.query = void 0, $;
  }
  function wv($) {
    if ($.port === (qB($) ? 443 : 80) || $.port === "") $.port = void 0;
    if (typeof $.secure === "boolean") $.scheme = $.secure ? "wss" : "ws", $.secure = void 0;
    if ($.resourceName) {
      let [X, J] = $.resourceName.split("?");
      $.path = X && X !== "/" ? X : void 0, $.query = J, $.resourceName = void 0;
    }
    return $.fragment = void 0, $;
  }
  function Bv($, X) {
    if (!$.path) return $.error = "URN can not be parsed", $;
    let J = $.path.match(Vv);
    if (J) {
      let Q = X.scheme || $.scheme || "urn";
      $.nid = J[1].toLowerCase(), $.nss = J[2];
      let Y = `${Q}:${X.nid || $.nid}`, z = $U[Y];
      if ($.path = void 0, z) $ = z.parse($, X);
    } else $.error = $.error || "URN can not be parsed.";
    return $;
  }
  function qv($, X) {
    let J = X.scheme || $.scheme || "urn", Q = $.nid.toLowerCase(), Y = `${J}:${X.nid || Q}`, z = $U[Y];
    if (z) $ = z.serialize($, X);
    let W = $, G = $.nss;
    return W.path = `${Q || X.nid}:${G}`, X.skipEscape = true, W;
  }
  function Dv($, X) {
    let J = $;
    if (J.uuid = J.nss, J.nss = void 0, !X.tolerant && (!J.uuid || !Nv.test(J.uuid))) J.error = J.error || "UUID is not valid.";
    return J;
  }
  function Lv($) {
    let X = $;
    return X.nss = ($.uuid || "").toLowerCase(), X;
  }
  var jB = { scheme: "http", domainHost: true, parse: DB, serialize: LB }, jv = { scheme: "https", domainHost: jB.domainHost, parse: DB, serialize: LB }, kQ = { scheme: "ws", domainHost: true, parse: Ov, serialize: wv }, Fv = { scheme: "wss", domainHost: kQ.domainHost, parse: kQ.parse, serialize: kQ.serialize }, Mv = { scheme: "urn", parse: Bv, serialize: qv, skipNormalize: true }, Iv = { scheme: "urn:uuid", parse: Dv, serialize: Lv, skipNormalize: true }, $U = { http: jB, https: jv, ws: kQ, wss: Fv, urn: Mv, "urn:uuid": Iv };
  FB.exports = $U;
});
var AB = k((Xt, xQ) => {
  var { normalizeIPv6: Av, normalizeIPv4: bv, removeDotSegments: _9, recomposeAuthority: Pv, normalizeComponentEncoding: _Q } = BB(), XU = MB();
  function Zv($, X) {
    if (typeof $ === "string") $ = n6(D4($, X), X);
    else if (typeof $ === "object") $ = D4(n6($, X), X);
    return $;
  }
  function Ev($, X, J) {
    let Q = Object.assign({ scheme: "null" }, J), Y = IB(D4($, Q), D4(X, Q), Q, true);
    return n6(Y, { ...Q, skipEscape: true });
  }
  function IB($, X, J, Q) {
    let Y = {};
    if (!Q) $ = D4(n6($, J), J), X = D4(n6(X, J), J);
    if (J = J || {}, !J.tolerant && X.scheme) Y.scheme = X.scheme, Y.userinfo = X.userinfo, Y.host = X.host, Y.port = X.port, Y.path = _9(X.path || ""), Y.query = X.query;
    else {
      if (X.userinfo !== void 0 || X.host !== void 0 || X.port !== void 0) Y.userinfo = X.userinfo, Y.host = X.host, Y.port = X.port, Y.path = _9(X.path || ""), Y.query = X.query;
      else {
        if (!X.path) if (Y.path = $.path, X.query !== void 0) Y.query = X.query;
        else Y.query = $.query;
        else {
          if (X.path.charAt(0) === "/") Y.path = _9(X.path);
          else {
            if (($.userinfo !== void 0 || $.host !== void 0 || $.port !== void 0) && !$.path) Y.path = "/" + X.path;
            else if (!$.path) Y.path = X.path;
            else Y.path = $.path.slice(0, $.path.lastIndexOf("/") + 1) + X.path;
            Y.path = _9(Y.path);
          }
          Y.query = X.query;
        }
        Y.userinfo = $.userinfo, Y.host = $.host, Y.port = $.port;
      }
      Y.scheme = $.scheme;
    }
    return Y.fragment = X.fragment, Y;
  }
  function Rv($, X, J) {
    if (typeof $ === "string") $ = unescape($), $ = n6(_Q(D4($, J), true), { ...J, skipEscape: true });
    else if (typeof $ === "object") $ = n6(_Q($, true), { ...J, skipEscape: true });
    if (typeof X === "string") X = unescape(X), X = n6(_Q(D4(X, J), true), { ...J, skipEscape: true });
    else if (typeof X === "object") X = n6(_Q(X, true), { ...J, skipEscape: true });
    return $.toLowerCase() === X.toLowerCase();
  }
  function n6($, X) {
    let J = { host: $.host, scheme: $.scheme, userinfo: $.userinfo, port: $.port, path: $.path, query: $.query, nid: $.nid, nss: $.nss, uuid: $.uuid, fragment: $.fragment, reference: $.reference, resourceName: $.resourceName, secure: $.secure, error: "" }, Q = Object.assign({}, X), Y = [], z = XU[(Q.scheme || J.scheme || "").toLowerCase()];
    if (z && z.serialize) z.serialize(J, Q);
    if (J.path !== void 0) if (!Q.skipEscape) {
      if (J.path = escape(J.path), J.scheme !== void 0) J.path = J.path.split("%3A").join(":");
    } else J.path = unescape(J.path);
    if (Q.reference !== "suffix" && J.scheme) Y.push(J.scheme, ":");
    let W = Pv(J);
    if (W !== void 0) {
      if (Q.reference !== "suffix") Y.push("//");
      if (Y.push(W), J.path && J.path.charAt(0) !== "/") Y.push("/");
    }
    if (J.path !== void 0) {
      let G = J.path;
      if (!Q.absolutePath && (!z || !z.absolutePath)) G = _9(G);
      if (W === void 0) G = G.replace(/^\/\//u, "/%2F");
      Y.push(G);
    }
    if (J.query !== void 0) Y.push("?", J.query);
    if (J.fragment !== void 0) Y.push("#", J.fragment);
    return Y.join("");
  }
  var Sv = Array.from({ length: 127 }, ($, X) => /[^!"$&'()*+,\-.;=_`a-z{}~]/u.test(String.fromCharCode(X)));
  function vv($) {
    let X = 0;
    for (let J = 0, Q = $.length; J < Q; ++J) if (X = $.charCodeAt(J), X > 126 || Sv[X]) return true;
    return false;
  }
  var Cv = /^(?:([^#/:?]+):)?(?:\/\/((?:([^#/?@]*)@)?(\[[^#/?\]]+\]|[^#/:?]*)(?::(\d*))?))?([^#?]*)(?:\?([^#]*))?(?:#((?:.|[\n\r])*))?/u;
  function D4($, X) {
    let J = Object.assign({}, X), Q = { scheme: void 0, userinfo: void 0, host: "", port: void 0, path: "", query: void 0, fragment: void 0 }, Y = $.indexOf("%") !== -1, z = false;
    if (J.reference === "suffix") $ = (J.scheme ? J.scheme + ":" : "") + "//" + $;
    let W = $.match(Cv);
    if (W) {
      if (Q.scheme = W[1], Q.userinfo = W[3], Q.host = W[4], Q.port = parseInt(W[5], 10), Q.path = W[6] || "", Q.query = W[7], Q.fragment = W[8], isNaN(Q.port)) Q.port = W[5];
      if (Q.host) {
        let U = bv(Q.host);
        if (U.isIPV4 === false) {
          let H = Av(U.host);
          Q.host = H.host.toLowerCase(), z = H.isIPV6;
        } else Q.host = U.host, z = true;
      }
      if (Q.scheme === void 0 && Q.userinfo === void 0 && Q.host === void 0 && Q.port === void 0 && Q.query === void 0 && !Q.path) Q.reference = "same-document";
      else if (Q.scheme === void 0) Q.reference = "relative";
      else if (Q.fragment === void 0) Q.reference = "absolute";
      else Q.reference = "uri";
      if (J.reference && J.reference !== "suffix" && J.reference !== Q.reference) Q.error = Q.error || "URI is not a " + J.reference + " reference.";
      let G = XU[(J.scheme || Q.scheme || "").toLowerCase()];
      if (!J.unicodeSupport && (!G || !G.unicodeSupport)) {
        if (Q.host && (J.domainHost || G && G.domainHost) && z === false && vv(Q.host)) try {
          Q.host = URL.domainToASCII(Q.host.toLowerCase());
        } catch (U) {
          Q.error = Q.error || "Host's domain name can not be converted to ASCII: " + U;
        }
      }
      if (!G || G && !G.skipNormalize) {
        if (Y && Q.scheme !== void 0) Q.scheme = unescape(Q.scheme);
        if (Y && Q.host !== void 0) Q.host = unescape(Q.host);
        if (Q.path) Q.path = escape(unescape(Q.path));
        if (Q.fragment) Q.fragment = encodeURI(decodeURIComponent(Q.fragment));
      }
      if (G && G.parse) G.parse(Q, J);
    } else Q.error = Q.error || "URI can not be parsed.";
    return Q;
  }
  var JU = { SCHEMES: XU, normalize: Zv, resolve: Ev, resolveComponents: IB, equal: Rv, serialize: n6, parse: D4 };
  xQ.exports = JU;
  xQ.exports.default = JU;
  xQ.exports.fastUri = JU;
});
var ZB = k((PB) => {
  Object.defineProperty(PB, "__esModule", { value: true });
  var bB = AB();
  bB.code = 'require("ajv/dist/runtime/uri").default';
  PB.default = bB;
});
var xB = k((L4) => {
  Object.defineProperty(L4, "__esModule", { value: true });
  L4.CodeGen = L4.Name = L4.nil = L4.stringify = L4.str = L4._ = L4.KeywordCxt = void 0;
  var _v = v9();
  Object.defineProperty(L4, "KeywordCxt", { enumerable: true, get: function() {
    return _v.KeywordCxt;
  } });
  var l0 = a();
  Object.defineProperty(L4, "_", { enumerable: true, get: function() {
    return l0._;
  } });
  Object.defineProperty(L4, "str", { enumerable: true, get: function() {
    return l0.str;
  } });
  Object.defineProperty(L4, "stringify", { enumerable: true, get: function() {
    return l0.stringify;
  } });
  Object.defineProperty(L4, "nil", { enumerable: true, get: function() {
    return l0.nil;
  } });
  Object.defineProperty(L4, "Name", { enumerable: true, get: function() {
    return l0.Name;
  } });
  Object.defineProperty(L4, "CodeGen", { enumerable: true, get: function() {
    return l0.CodeGen;
  } });
  var xv = SQ(), CB = C9(), Tv = y3(), x9 = CQ(), yv = a(), T9 = E9(), TQ = Z9(), QU = Q$(), EB = zB(), fv = ZB(), kB = ($, X) => new RegExp($, X);
  kB.code = "new RegExp";
  var gv = ["removeAdditional", "useDefaults", "coerceTypes"], hv = /* @__PURE__ */ new Set(["validate", "serialize", "parse", "wrapper", "root", "schema", "keyword", "pattern", "formats", "validate$data", "func", "obj", "Error"]), uv = { errorDataPath: "", format: "`validateFormats: false` can be used instead.", nullable: '"nullable" keyword is supported by default.', jsonPointers: "Deprecated jsPropertySyntax can be used instead.", extendRefs: "Deprecated ignoreKeywordsWithRef can be used instead.", missingRefs: "Pass empty schema with $id that should be ignored to ajv.addSchema.", processCode: "Use option `code: {process: (code, schemaEnv: object) => string}`", sourceCode: "Use option `code: {source: true}`", strictDefaults: "It is default now, see option `strict`.", strictKeywords: "It is default now, see option `strict`.", uniqueItems: '"uniqueItems" keyword is always validated.', unknownFormats: "Disable strict mode or pass `true` to `ajv.addFormat` (or `formats` option).", cache: "Map is used as cache, schema object as key.", serialize: "Map is used as cache, schema object as key.", ajvErrors: "It is default now." }, mv = { ignoreKeywordsWithRef: "", jsPropertySyntax: "", unicode: '"minLength"/"maxLength" account for unicode characters by default.' }, RB = 200;
  function lv($) {
    var X, J, Q, Y, z, W, G, U, H, K, V, O, N, w, B, L, j, I, b, x, h, B$, x$, G6, o6;
    let u6 = $.strict, a4 = (X = $.code) === null || X === void 0 ? void 0 : X.optimize, _1 = a4 === true || a4 === void 0 ? 1 : a4 || 0, t6 = (Q = (J = $.code) === null || J === void 0 ? void 0 : J.regExp) !== null && Q !== void 0 ? Q : kB, r0 = (Y = $.uriResolver) !== null && Y !== void 0 ? Y : fv.default;
    return { strictSchema: (W = (z = $.strictSchema) !== null && z !== void 0 ? z : u6) !== null && W !== void 0 ? W : true, strictNumbers: (U = (G = $.strictNumbers) !== null && G !== void 0 ? G : u6) !== null && U !== void 0 ? U : true, strictTypes: (K = (H = $.strictTypes) !== null && H !== void 0 ? H : u6) !== null && K !== void 0 ? K : "log", strictTuples: (O = (V = $.strictTuples) !== null && V !== void 0 ? V : u6) !== null && O !== void 0 ? O : "log", strictRequired: (w = (N = $.strictRequired) !== null && N !== void 0 ? N : u6) !== null && w !== void 0 ? w : false, code: $.code ? { ...$.code, optimize: _1, regExp: t6 } : { optimize: _1, regExp: t6 }, loopRequired: (B = $.loopRequired) !== null && B !== void 0 ? B : RB, loopEnum: (L = $.loopEnum) !== null && L !== void 0 ? L : RB, meta: (j = $.meta) !== null && j !== void 0 ? j : true, messages: (I = $.messages) !== null && I !== void 0 ? I : true, inlineRefs: (b = $.inlineRefs) !== null && b !== void 0 ? b : true, schemaId: (x = $.schemaId) !== null && x !== void 0 ? x : "$id", addUsedSchema: (h = $.addUsedSchema) !== null && h !== void 0 ? h : true, validateSchema: (B$ = $.validateSchema) !== null && B$ !== void 0 ? B$ : true, validateFormats: (x$ = $.validateFormats) !== null && x$ !== void 0 ? x$ : true, unicodeRegExp: (G6 = $.unicodeRegExp) !== null && G6 !== void 0 ? G6 : true, int32range: (o6 = $.int32range) !== null && o6 !== void 0 ? o6 : true, uriResolver: r0 };
  }
  class yQ {
    constructor($ = {}) {
      this.schemas = {}, this.refs = {}, this.formats = {}, this._compilations = /* @__PURE__ */ new Set(), this._loading = {}, this._cache = /* @__PURE__ */ new Map(), $ = this.opts = { ...$, ...lv($) };
      let { es5: X, lines: J } = this.opts.code;
      this.scope = new yv.ValueScope({ scope: {}, prefixes: hv, es5: X, lines: J }), this.logger = rv($.logger);
      let Q = $.validateFormats;
      if ($.validateFormats = false, this.RULES = (0, Tv.getRules)(), SB.call(this, uv, $, "NOT SUPPORTED"), SB.call(this, mv, $, "DEPRECATED", "warn"), this._metaOpts = nv.call(this), $.formats) pv.call(this);
      if (this._addVocabularies(), this._addDefaultMetaSchema(), $.keywords) iv.call(this, $.keywords);
      if (typeof $.meta == "object") this.addMetaSchema($.meta);
      cv.call(this), $.validateFormats = Q;
    }
    _addVocabularies() {
      this.addKeyword("$async");
    }
    _addDefaultMetaSchema() {
      let { $data: $, meta: X, schemaId: J } = this.opts, Q = EB;
      if (J === "id") Q = { ...EB }, Q.id = Q.$id, delete Q.$id;
      if (X && $) this.addMetaSchema(Q, Q[J], false);
    }
    defaultMeta() {
      let { meta: $, schemaId: X } = this.opts;
      return this.opts.defaultMeta = typeof $ == "object" ? $[X] || $ : void 0;
    }
    validate($, X) {
      let J;
      if (typeof $ == "string") {
        if (J = this.getSchema($), !J) throw Error(`no schema with key or ref "${$}"`);
      } else J = this.compile($);
      let Q = J(X);
      if (!("$async" in J)) this.errors = J.errors;
      return Q;
    }
    compile($, X) {
      let J = this._addSchema($, X);
      return J.validate || this._compileSchemaEnv(J);
    }
    compileAsync($, X) {
      if (typeof this.opts.loadSchema != "function") throw Error("options.loadSchema should be a function");
      let { loadSchema: J } = this.opts;
      return Q.call(this, $, X);
      async function Q(H, K) {
        await Y.call(this, H.$schema);
        let V = this._addSchema(H, K);
        return V.validate || z.call(this, V);
      }
      async function Y(H) {
        if (H && !this.getSchema(H)) await Q.call(this, { $ref: H }, true);
      }
      async function z(H) {
        try {
          return this._compileSchemaEnv(H);
        } catch (K) {
          if (!(K instanceof CB.default)) throw K;
          return W.call(this, K), await G.call(this, K.missingSchema), z.call(this, H);
        }
      }
      function W({ missingSchema: H, missingRef: K }) {
        if (this.refs[H]) throw Error(`AnySchema ${H} is loaded but ${K} cannot be resolved`);
      }
      async function G(H) {
        let K = await U.call(this, H);
        if (!this.refs[H]) await Y.call(this, K.$schema);
        if (!this.refs[H]) this.addSchema(K, H, X);
      }
      async function U(H) {
        let K = this._loading[H];
        if (K) return K;
        try {
          return await (this._loading[H] = J(H));
        } finally {
          delete this._loading[H];
        }
      }
    }
    addSchema($, X, J, Q = this.opts.validateSchema) {
      if (Array.isArray($)) {
        for (let z of $) this.addSchema(z, void 0, J, Q);
        return this;
      }
      let Y;
      if (typeof $ === "object") {
        let { schemaId: z } = this.opts;
        if (Y = $[z], Y !== void 0 && typeof Y != "string") throw Error(`schema ${z} must be string`);
      }
      return X = (0, T9.normalizeId)(X || Y), this._checkUnique(X), this.schemas[X] = this._addSchema($, J, X, Q, true), this;
    }
    addMetaSchema($, X, J = this.opts.validateSchema) {
      return this.addSchema($, X, true, J), this;
    }
    validateSchema($, X) {
      if (typeof $ == "boolean") return true;
      let J;
      if (J = $.$schema, J !== void 0 && typeof J != "string") throw Error("$schema must be a string");
      if (J = J || this.opts.defaultMeta || this.defaultMeta(), !J) return this.logger.warn("meta-schema not available"), this.errors = null, true;
      let Q = this.validate(J, $);
      if (!Q && X) {
        let Y = "schema is invalid: " + this.errorsText();
        if (this.opts.validateSchema === "log") this.logger.error(Y);
        else throw Error(Y);
      }
      return Q;
    }
    getSchema($) {
      let X;
      while (typeof (X = vB.call(this, $)) == "string") $ = X;
      if (X === void 0) {
        let { schemaId: J } = this.opts, Q = new x9.SchemaEnv({ schema: {}, schemaId: J });
        if (X = x9.resolveSchema.call(this, Q, $), !X) return;
        this.refs[$] = X;
      }
      return X.validate || this._compileSchemaEnv(X);
    }
    removeSchema($) {
      if ($ instanceof RegExp) return this._removeAllSchemas(this.schemas, $), this._removeAllSchemas(this.refs, $), this;
      switch (typeof $) {
        case "undefined":
          return this._removeAllSchemas(this.schemas), this._removeAllSchemas(this.refs), this._cache.clear(), this;
        case "string": {
          let X = vB.call(this, $);
          if (typeof X == "object") this._cache.delete(X.schema);
          return delete this.schemas[$], delete this.refs[$], this;
        }
        case "object": {
          let X = $;
          this._cache.delete(X);
          let J = $[this.opts.schemaId];
          if (J) J = (0, T9.normalizeId)(J), delete this.schemas[J], delete this.refs[J];
          return this;
        }
        default:
          throw Error("ajv.removeSchema: invalid parameter");
      }
    }
    addVocabulary($) {
      for (let X of $) this.addKeyword(X);
      return this;
    }
    addKeyword($, X) {
      let J;
      if (typeof $ == "string") {
        if (J = $, typeof X == "object") this.logger.warn("these parameters are deprecated, see docs for addKeyword"), X.keyword = J;
      } else if (typeof $ == "object" && X === void 0) {
        if (X = $, J = X.keyword, Array.isArray(J) && !J.length) throw Error("addKeywords: keyword must be string or non-empty array");
      } else throw Error("invalid addKeywords parameters");
      if (tv.call(this, J, X), !X) return (0, QU.eachItem)(J, (Y) => YU.call(this, Y)), this;
      sv.call(this, X);
      let Q = { ...X, type: (0, TQ.getJSONTypes)(X.type), schemaType: (0, TQ.getJSONTypes)(X.schemaType) };
      return (0, QU.eachItem)(J, Q.type.length === 0 ? (Y) => YU.call(this, Y, Q) : (Y) => Q.type.forEach((z) => YU.call(this, Y, Q, z))), this;
    }
    getKeyword($) {
      let X = this.RULES.all[$];
      return typeof X == "object" ? X.definition : !!X;
    }
    removeKeyword($) {
      let { RULES: X } = this;
      delete X.keywords[$], delete X.all[$];
      for (let J of X.rules) {
        let Q = J.rules.findIndex((Y) => Y.keyword === $);
        if (Q >= 0) J.rules.splice(Q, 1);
      }
      return this;
    }
    addFormat($, X) {
      if (typeof X == "string") X = new RegExp(X);
      return this.formats[$] = X, this;
    }
    errorsText($ = this.errors, { separator: X = ", ", dataVar: J = "data" } = {}) {
      if (!$ || $.length === 0) return "No errors";
      return $.map((Q) => `${J}${Q.instancePath} ${Q.message}`).reduce((Q, Y) => Q + X + Y);
    }
    $dataMetaSchema($, X) {
      let J = this.RULES.all;
      $ = JSON.parse(JSON.stringify($));
      for (let Q of X) {
        let Y = Q.split("/").slice(1), z = $;
        for (let W of Y) z = z[W];
        for (let W in J) {
          let G = J[W];
          if (typeof G != "object") continue;
          let { $data: U } = G.definition, H = z[W];
          if (U && H) z[W] = _B(H);
        }
      }
      return $;
    }
    _removeAllSchemas($, X) {
      for (let J in $) {
        let Q = $[J];
        if (!X || X.test(J)) {
          if (typeof Q == "string") delete $[J];
          else if (Q && !Q.meta) this._cache.delete(Q.schema), delete $[J];
        }
      }
    }
    _addSchema($, X, J, Q = this.opts.validateSchema, Y = this.opts.addUsedSchema) {
      let z, { schemaId: W } = this.opts;
      if (typeof $ == "object") z = $[W];
      else if (this.opts.jtd) throw Error("schema must be object");
      else if (typeof $ != "boolean") throw Error("schema must be object or boolean");
      let G = this._cache.get($);
      if (G !== void 0) return G;
      J = (0, T9.normalizeId)(z || J);
      let U = T9.getSchemaRefs.call(this, $, J);
      if (G = new x9.SchemaEnv({ schema: $, schemaId: W, meta: X, baseId: J, localRefs: U }), this._cache.set(G.schema, G), Y && !J.startsWith("#")) {
        if (J) this._checkUnique(J);
        this.refs[J] = G;
      }
      if (Q) this.validateSchema($, true);
      return G;
    }
    _checkUnique($) {
      if (this.schemas[$] || this.refs[$]) throw Error(`schema with key or id "${$}" already exists`);
    }
    _compileSchemaEnv($) {
      if ($.meta) this._compileMetaSchema($);
      else x9.compileSchema.call(this, $);
      if (!$.validate) throw Error("ajv implementation error");
      return $.validate;
    }
    _compileMetaSchema($) {
      let X = this.opts;
      this.opts = this._metaOpts;
      try {
        x9.compileSchema.call(this, $);
      } finally {
        this.opts = X;
      }
    }
  }
  yQ.ValidationError = xv.default;
  yQ.MissingRefError = CB.default;
  L4.default = yQ;
  function SB($, X, J, Q = "error") {
    for (let Y in $) {
      let z = Y;
      if (z in X) this.logger[Q](`${J}: option ${Y}. ${$[z]}`);
    }
  }
  function vB($) {
    return $ = (0, T9.normalizeId)($), this.schemas[$] || this.refs[$];
  }
  function cv() {
    let $ = this.opts.schemas;
    if (!$) return;
    if (Array.isArray($)) this.addSchema($);
    else for (let X in $) this.addSchema($[X], X);
  }
  function pv() {
    for (let $ in this.opts.formats) {
      let X = this.opts.formats[$];
      if (X) this.addFormat($, X);
    }
  }
  function iv($) {
    if (Array.isArray($)) {
      this.addVocabulary($);
      return;
    }
    this.logger.warn("keywords option as map is deprecated, pass array");
    for (let X in $) {
      let J = $[X];
      if (!J.keyword) J.keyword = X;
      this.addKeyword(J);
    }
  }
  function nv() {
    let $ = { ...this.opts };
    for (let X of gv) delete $[X];
    return $;
  }
  var dv = { log() {
  }, warn() {
  }, error() {
  } };
  function rv($) {
    if ($ === false) return dv;
    if ($ === void 0) return console;
    if ($.log && $.warn && $.error) return $;
    throw Error("logger must implement log, warn and error methods");
  }
  var ov = /^[a-z_$][a-z0-9_$:-]*$/i;
  function tv($, X) {
    let { RULES: J } = this;
    if ((0, QU.eachItem)($, (Q) => {
      if (J.keywords[Q]) throw Error(`Keyword ${Q} is already defined`);
      if (!ov.test(Q)) throw Error(`Keyword ${Q} has invalid name`);
    }), !X) return;
    if (X.$data && !("code" in X || "validate" in X)) throw Error('$data keyword must have "code" or "validate" function');
  }
  function YU($, X, J) {
    var Q;
    let Y = X === null || X === void 0 ? void 0 : X.post;
    if (J && Y) throw Error('keyword with "post" flag cannot have "type"');
    let { RULES: z } = this, W = Y ? z.post : z.rules.find(({ type: U }) => U === J);
    if (!W) W = { type: J, rules: [] }, z.rules.push(W);
    if (z.keywords[$] = true, !X) return;
    let G = { keyword: $, definition: { ...X, type: (0, TQ.getJSONTypes)(X.type), schemaType: (0, TQ.getJSONTypes)(X.schemaType) } };
    if (X.before) av.call(this, W, G, X.before);
    else W.rules.push(G);
    z.all[$] = G, (Q = X.implements) === null || Q === void 0 || Q.forEach((U) => this.addKeyword(U));
  }
  function av($, X, J) {
    let Q = $.rules.findIndex((Y) => Y.keyword === J);
    if (Q >= 0) $.rules.splice(Q, 0, X);
    else $.rules.push(X), this.logger.warn(`rule ${J} is not defined`);
  }
  function sv($) {
    let { metaSchema: X } = $;
    if (X === void 0) return;
    if ($.$data && this.opts.$data) X = _B(X);
    $.validateSchema = this.compile(X, true);
  }
  var ev = { $ref: "https://raw.githubusercontent.com/ajv-validator/ajv/master/lib/refs/data.json#" };
  function _B($) {
    return { anyOf: [$, ev] };
  }
});
var yB = k((TB) => {
  Object.defineProperty(TB, "__esModule", { value: true });
  var JC = { keyword: "id", code() {
    throw Error('NOT SUPPORTED: keyword "id", use "$id" for schema ID');
  } };
  TB.default = JC;
});
var lB = k((uB) => {
  Object.defineProperty(uB, "__esModule", { value: true });
  uB.callRef = uB.getValidate = void 0;
  var QC = C9(), fB = Z6(), z6 = a(), c0 = B4(), gB = CQ(), fQ = Q$(), zC = { keyword: "$ref", schemaType: "string", code($) {
    let { gen: X, schema: J, it: Q } = $, { baseId: Y, schemaEnv: z, validateName: W, opts: G, self: U } = Q, { root: H } = z;
    if ((J === "#" || J === "#/") && Y === H.baseId) return V();
    let K = gB.resolveRef.call(U, H, Y, J);
    if (K === void 0) throw new QC.default(Q.opts.uriResolver, Y, J);
    if (K instanceof gB.SchemaEnv) return O(K);
    return N(K);
    function V() {
      if (z === H) return gQ($, W, z, z.$async);
      let w = X.scopeValue("root", { ref: H });
      return gQ($, z6._`${w}.validate`, H, H.$async);
    }
    function O(w) {
      let B = hB($, w);
      gQ($, B, w, w.$async);
    }
    function N(w) {
      let B = X.scopeValue("schema", G.code.source === true ? { ref: w, code: (0, z6.stringify)(w) } : { ref: w }), L = X.name("valid"), j = $.subschema({ schema: w, dataTypes: [], schemaPath: z6.nil, topSchemaRef: B, errSchemaPath: J }, L);
      $.mergeEvaluated(j), $.ok(L);
    }
  } };
  function hB($, X) {
    let { gen: J } = $;
    return X.validate ? J.scopeValue("validate", { ref: X.validate }) : z6._`${J.scopeValue("wrapper", { ref: X })}.validate`;
  }
  uB.getValidate = hB;
  function gQ($, X, J, Q) {
    let { gen: Y, it: z } = $, { allErrors: W, schemaEnv: G, opts: U } = z, H = U.passContext ? c0.default.this : z6.nil;
    if (Q) K();
    else V();
    function K() {
      if (!G.$async) throw Error("async schema referenced by sync schema");
      let w = Y.let("valid");
      Y.try(() => {
        if (Y.code(z6._`await ${(0, fB.callValidateCode)($, X, H)}`), N(X), !W) Y.assign(w, true);
      }, (B) => {
        if (Y.if(z6._`!(${B} instanceof ${z.ValidationError})`, () => Y.throw(B)), O(B), !W) Y.assign(w, false);
      }), $.ok(w);
    }
    function V() {
      $.result((0, fB.callValidateCode)($, X, H), () => N(X), () => O(X));
    }
    function O(w) {
      let B = z6._`${w}.errors`;
      Y.assign(c0.default.vErrors, z6._`${c0.default.vErrors} === null ? ${B} : ${c0.default.vErrors}.concat(${B})`), Y.assign(c0.default.errors, z6._`${c0.default.vErrors}.length`);
    }
    function N(w) {
      var B;
      if (!z.opts.unevaluated) return;
      let L = (B = J === null || J === void 0 ? void 0 : J.validate) === null || B === void 0 ? void 0 : B.evaluated;
      if (z.props !== true) if (L && !L.dynamicProps) {
        if (L.props !== void 0) z.props = fQ.mergeEvaluated.props(Y, L.props, z.props);
      } else {
        let j = Y.var("props", z6._`${w}.evaluated.props`);
        z.props = fQ.mergeEvaluated.props(Y, j, z.props, z6.Name);
      }
      if (z.items !== true) if (L && !L.dynamicItems) {
        if (L.items !== void 0) z.items = fQ.mergeEvaluated.items(Y, L.items, z.items);
      } else {
        let j = Y.var("items", z6._`${w}.evaluated.items`);
        z.items = fQ.mergeEvaluated.items(Y, j, z.items, z6.Name);
      }
    }
  }
  uB.callRef = gQ;
  uB.default = zC;
});
var pB = k((cB) => {
  Object.defineProperty(cB, "__esModule", { value: true });
  var UC = yB(), HC = lB(), KC = ["$schema", "$id", "$defs", "$vocabulary", { keyword: "$comment" }, "definitions", UC.default, HC.default];
  cB.default = KC;
});
var nB = k((iB) => {
  Object.defineProperty(iB, "__esModule", { value: true });
  var hQ = a(), r4 = hQ.operators, uQ = { maximum: { okStr: "<=", ok: r4.LTE, fail: r4.GT }, minimum: { okStr: ">=", ok: r4.GTE, fail: r4.LT }, exclusiveMaximum: { okStr: "<", ok: r4.LT, fail: r4.GTE }, exclusiveMinimum: { okStr: ">", ok: r4.GT, fail: r4.LTE } }, VC = { message: ({ keyword: $, schemaCode: X }) => hQ.str`must be ${uQ[$].okStr} ${X}`, params: ({ keyword: $, schemaCode: X }) => hQ._`{comparison: ${uQ[$].okStr}, limit: ${X}}` }, OC = { keyword: Object.keys(uQ), type: "number", schemaType: "number", $data: true, error: VC, code($) {
    let { keyword: X, data: J, schemaCode: Q } = $;
    $.fail$data(hQ._`${J} ${uQ[X].fail} ${Q} || isNaN(${J})`);
  } };
  iB.default = OC;
});
var rB = k((dB) => {
  Object.defineProperty(dB, "__esModule", { value: true });
  var y9 = a(), BC = { message: ({ schemaCode: $ }) => y9.str`must be multiple of ${$}`, params: ({ schemaCode: $ }) => y9._`{multipleOf: ${$}}` }, qC = { keyword: "multipleOf", type: "number", schemaType: "number", $data: true, error: BC, code($) {
    let { gen: X, data: J, schemaCode: Q, it: Y } = $, z = Y.opts.multipleOfPrecision, W = X.let("res"), G = z ? y9._`Math.abs(Math.round(${W}) - ${W}) > 1e-${z}` : y9._`${W} !== parseInt(${W})`;
    $.fail$data(y9._`(${Q} === 0 || (${W} = ${J}/${Q}, ${G}))`);
  } };
  dB.default = qC;
});
var aB = k((tB) => {
  Object.defineProperty(tB, "__esModule", { value: true });
  function oB($) {
    let X = $.length, J = 0, Q = 0, Y;
    while (Q < X) if (J++, Y = $.charCodeAt(Q++), Y >= 55296 && Y <= 56319 && Q < X) {
      if (Y = $.charCodeAt(Q), (Y & 64512) === 56320) Q++;
    }
    return J;
  }
  tB.default = oB;
  oB.code = 'require("ajv/dist/runtime/ucs2length").default';
});
var eB = k((sB) => {
  Object.defineProperty(sB, "__esModule", { value: true });
  var C1 = a(), jC = Q$(), FC = aB(), MC = { message({ keyword: $, schemaCode: X }) {
    let J = $ === "maxLength" ? "more" : "fewer";
    return C1.str`must NOT have ${J} than ${X} characters`;
  }, params: ({ schemaCode: $ }) => C1._`{limit: ${$}}` }, IC = { keyword: ["maxLength", "minLength"], type: "string", schemaType: "number", $data: true, error: MC, code($) {
    let { keyword: X, data: J, schemaCode: Q, it: Y } = $, z = X === "maxLength" ? C1.operators.GT : C1.operators.LT, W = Y.opts.unicode === false ? C1._`${J}.length` : C1._`${(0, jC.useFunc)($.gen, FC.default)}(${J})`;
    $.fail$data(C1._`${W} ${z} ${Q}`);
  } };
  sB.default = IC;
});
var Xq = k(($q) => {
  Object.defineProperty($q, "__esModule", { value: true });
  var bC = Z6(), PC = Q$(), p0 = a(), ZC = { message: ({ schemaCode: $ }) => p0.str`must match pattern "${$}"`, params: ({ schemaCode: $ }) => p0._`{pattern: ${$}}` }, EC = { keyword: "pattern", type: "string", schemaType: "string", $data: true, error: ZC, code($) {
    let { gen: X, data: J, $data: Q, schema: Y, schemaCode: z, it: W } = $, G = W.opts.unicodeRegExp ? "u" : "";
    if (Q) {
      let { regExp: U } = W.opts.code, H = U.code === "new RegExp" ? p0._`new RegExp` : (0, PC.useFunc)(X, U), K = X.let("valid");
      X.try(() => X.assign(K, p0._`${H}(${z}, ${G}).test(${J})`), () => X.assign(K, false)), $.fail$data(p0._`!${K}`);
    } else {
      let U = (0, bC.usePattern)($, Y);
      $.fail$data(p0._`!${U}.test(${J})`);
    }
  } };
  $q.default = EC;
});
var Yq = k((Jq) => {
  Object.defineProperty(Jq, "__esModule", { value: true });
  var f9 = a(), SC = { message({ keyword: $, schemaCode: X }) {
    let J = $ === "maxProperties" ? "more" : "fewer";
    return f9.str`must NOT have ${J} than ${X} properties`;
  }, params: ({ schemaCode: $ }) => f9._`{limit: ${$}}` }, vC = { keyword: ["maxProperties", "minProperties"], type: "object", schemaType: "number", $data: true, error: SC, code($) {
    let { keyword: X, data: J, schemaCode: Q } = $, Y = X === "maxProperties" ? f9.operators.GT : f9.operators.LT;
    $.fail$data(f9._`Object.keys(${J}).length ${Y} ${Q}`);
  } };
  Jq.default = vC;
});
var zq = k((Qq) => {
  Object.defineProperty(Qq, "__esModule", { value: true });
  var g9 = Z6(), h9 = a(), kC = Q$(), _C = { message: ({ params: { missingProperty: $ } }) => h9.str`must have required property '${$}'`, params: ({ params: { missingProperty: $ } }) => h9._`{missingProperty: ${$}}` }, xC = { keyword: "required", type: "object", schemaType: "array", $data: true, error: _C, code($) {
    let { gen: X, schema: J, schemaCode: Q, data: Y, $data: z, it: W } = $, { opts: G } = W;
    if (!z && J.length === 0) return;
    let U = J.length >= G.loopRequired;
    if (W.allErrors) H();
    else K();
    if (G.strictRequired) {
      let N = $.parentSchema.properties, { definedProperties: w } = $.it;
      for (let B of J) if ((N === null || N === void 0 ? void 0 : N[B]) === void 0 && !w.has(B)) {
        let L = W.schemaEnv.baseId + W.errSchemaPath, j = `required property "${B}" is not defined at "${L}" (strictRequired)`;
        (0, kC.checkStrictMode)(W, j, W.opts.strictRequired);
      }
    }
    function H() {
      if (U || z) $.block$data(h9.nil, V);
      else for (let N of J) (0, g9.checkReportMissingProp)($, N);
    }
    function K() {
      let N = X.let("missing");
      if (U || z) {
        let w = X.let("valid", true);
        $.block$data(w, () => O(N, w)), $.ok(w);
      } else X.if((0, g9.checkMissingProp)($, J, N)), (0, g9.reportMissingProp)($, N), X.else();
    }
    function V() {
      X.forOf("prop", Q, (N) => {
        $.setParams({ missingProperty: N }), X.if((0, g9.noPropertyInData)(X, Y, N, G.ownProperties), () => $.error());
      });
    }
    function O(N, w) {
      $.setParams({ missingProperty: N }), X.forOf(N, Q, () => {
        X.assign(w, (0, g9.propertyInData)(X, Y, N, G.ownProperties)), X.if((0, h9.not)(w), () => {
          $.error(), X.break();
        });
      }, h9.nil);
    }
  } };
  Qq.default = xC;
});
var Gq = k((Wq) => {
  Object.defineProperty(Wq, "__esModule", { value: true });
  var u9 = a(), yC = { message({ keyword: $, schemaCode: X }) {
    let J = $ === "maxItems" ? "more" : "fewer";
    return u9.str`must NOT have ${J} than ${X} items`;
  }, params: ({ schemaCode: $ }) => u9._`{limit: ${$}}` }, fC = { keyword: ["maxItems", "minItems"], type: "array", schemaType: "number", $data: true, error: yC, code($) {
    let { keyword: X, data: J, schemaCode: Q } = $, Y = X === "maxItems" ? u9.operators.GT : u9.operators.LT;
    $.fail$data(u9._`${J}.length ${Y} ${Q}`);
  } };
  Wq.default = fC;
});
var mQ = k((Hq) => {
  Object.defineProperty(Hq, "__esModule", { value: true });
  var Uq = p3();
  Uq.code = 'require("ajv/dist/runtime/equal").default';
  Hq.default = Uq;
});
var Nq = k((Kq) => {
  Object.defineProperty(Kq, "__esModule", { value: true });
  var zU = Z9(), h$ = a(), uC = Q$(), mC = mQ(), lC = { message: ({ params: { i: $, j: X } }) => h$.str`must NOT have duplicate items (items ## ${X} and ${$} are identical)`, params: ({ params: { i: $, j: X } }) => h$._`{i: ${$}, j: ${X}}` }, cC = { keyword: "uniqueItems", type: "array", schemaType: "boolean", $data: true, error: lC, code($) {
    let { gen: X, data: J, $data: Q, schema: Y, parentSchema: z, schemaCode: W, it: G } = $;
    if (!Q && !Y) return;
    let U = X.let("valid"), H = z.items ? (0, zU.getSchemaTypes)(z.items) : [];
    $.block$data(U, K, h$._`${W} === false`), $.ok(U);
    function K() {
      let w = X.let("i", h$._`${J}.length`), B = X.let("j");
      $.setParams({ i: w, j: B }), X.assign(U, true), X.if(h$._`${w} > 1`, () => (V() ? O : N)(w, B));
    }
    function V() {
      return H.length > 0 && !H.some((w) => w === "object" || w === "array");
    }
    function O(w, B) {
      let L = X.name("item"), j = (0, zU.checkDataTypes)(H, L, G.opts.strictNumbers, zU.DataType.Wrong), I = X.const("indices", h$._`{}`);
      X.for(h$._`;${w}--;`, () => {
        if (X.let(L, h$._`${J}[${w}]`), X.if(j, h$._`continue`), H.length > 1) X.if(h$._`typeof ${L} == "string"`, h$._`${L} += "_"`);
        X.if(h$._`typeof ${I}[${L}] == "number"`, () => {
          X.assign(B, h$._`${I}[${L}]`), $.error(), X.assign(U, false).break();
        }).code(h$._`${I}[${L}] = ${w}`);
      });
    }
    function N(w, B) {
      let L = (0, uC.useFunc)(X, mC.default), j = X.name("outer");
      X.label(j).for(h$._`;${w}--;`, () => X.for(h$._`${B} = ${w}; ${B}--;`, () => X.if(h$._`${L}(${J}[${w}], ${J}[${B}])`, () => {
        $.error(), X.assign(U, false).break(j);
      })));
    }
  } };
  Kq.default = cC;
});
var Oq = k((Vq) => {
  Object.defineProperty(Vq, "__esModule", { value: true });
  var WU = a(), iC = Q$(), nC = mQ(), dC = { message: "must be equal to constant", params: ({ schemaCode: $ }) => WU._`{allowedValue: ${$}}` }, rC = { keyword: "const", $data: true, error: dC, code($) {
    let { gen: X, data: J, $data: Q, schemaCode: Y, schema: z } = $;
    if (Q || z && typeof z == "object") $.fail$data(WU._`!${(0, iC.useFunc)(X, nC.default)}(${J}, ${Y})`);
    else $.fail(WU._`${z} !== ${J}`);
  } };
  Vq.default = rC;
});
var Bq = k((wq) => {
  Object.defineProperty(wq, "__esModule", { value: true });
  var m9 = a(), tC = Q$(), aC = mQ(), sC = { message: "must be equal to one of the allowed values", params: ({ schemaCode: $ }) => m9._`{allowedValues: ${$}}` }, eC = { keyword: "enum", schemaType: "array", $data: true, error: sC, code($) {
    let { gen: X, data: J, $data: Q, schema: Y, schemaCode: z, it: W } = $;
    if (!Q && Y.length === 0) throw Error("enum must have non-empty array");
    let G = Y.length >= W.opts.loopEnum, U, H = () => U !== null && U !== void 0 ? U : U = (0, tC.useFunc)(X, aC.default), K;
    if (G || Q) K = X.let("valid"), $.block$data(K, V);
    else {
      if (!Array.isArray(Y)) throw Error("ajv implementation error");
      let N = X.const("vSchema", z);
      K = (0, m9.or)(...Y.map((w, B) => O(N, B)));
    }
    $.pass(K);
    function V() {
      X.assign(K, false), X.forOf("v", z, (N) => X.if(m9._`${H()}(${J}, ${N})`, () => X.assign(K, true).break()));
    }
    function O(N, w) {
      let B = Y[w];
      return typeof B === "object" && B !== null ? m9._`${H()}(${J}, ${N}[${w}])` : m9._`${J} === ${B}`;
    }
  } };
  wq.default = eC;
});
var Dq = k((qq) => {
  Object.defineProperty(qq, "__esModule", { value: true });
  var Xk = nB(), Jk = rB(), Yk = eB(), Qk = Xq(), zk = Yq(), Wk = zq(), Gk = Gq(), Uk = Nq(), Hk = Oq(), Kk = Bq(), Nk = [Xk.default, Jk.default, Yk.default, Qk.default, zk.default, Wk.default, Gk.default, Uk.default, { keyword: "type", schemaType: ["string", "array"] }, { keyword: "nullable", schemaType: "boolean" }, Hk.default, Kk.default];
  qq.default = Nk;
});
var UU = k((jq) => {
  Object.defineProperty(jq, "__esModule", { value: true });
  jq.validateAdditionalItems = void 0;
  var k1 = a(), GU = Q$(), Ok = { message: ({ params: { len: $ } }) => k1.str`must NOT have more than ${$} items`, params: ({ params: { len: $ } }) => k1._`{limit: ${$}}` }, wk = { keyword: "additionalItems", type: "array", schemaType: ["boolean", "object"], before: "uniqueItems", error: Ok, code($) {
    let { parentSchema: X, it: J } = $, { items: Q } = X;
    if (!Array.isArray(Q)) {
      (0, GU.checkStrictMode)(J, '"additionalItems" is ignored when "items" is not an array of schemas');
      return;
    }
    Lq($, Q);
  } };
  function Lq($, X) {
    let { gen: J, schema: Q, data: Y, keyword: z, it: W } = $;
    W.items = true;
    let G = J.const("len", k1._`${Y}.length`);
    if (Q === false) $.setParams({ len: X.length }), $.pass(k1._`${G} <= ${X.length}`);
    else if (typeof Q == "object" && !(0, GU.alwaysValidSchema)(W, Q)) {
      let H = J.var("valid", k1._`${G} <= ${X.length}`);
      J.if((0, k1.not)(H), () => U(H)), $.ok(H);
    }
    function U(H) {
      J.forRange("i", X.length, G, (K) => {
        if ($.subschema({ keyword: z, dataProp: K, dataPropType: GU.Type.Num }, H), !W.allErrors) J.if((0, k1.not)(H), () => J.break());
      });
    }
  }
  jq.validateAdditionalItems = Lq;
  jq.default = wk;
});
var HU = k((Aq) => {
  Object.defineProperty(Aq, "__esModule", { value: true });
  Aq.validateTuple = void 0;
  var Mq = a(), lQ = Q$(), qk = Z6(), Dk = { keyword: "items", type: "array", schemaType: ["object", "array", "boolean"], before: "uniqueItems", code($) {
    let { schema: X, it: J } = $;
    if (Array.isArray(X)) return Iq($, "additionalItems", X);
    if (J.items = true, (0, lQ.alwaysValidSchema)(J, X)) return;
    $.ok((0, qk.validateArray)($));
  } };
  function Iq($, X, J = $.schema) {
    let { gen: Q, parentSchema: Y, data: z, keyword: W, it: G } = $;
    if (K(Y), G.opts.unevaluated && J.length && G.items !== true) G.items = lQ.mergeEvaluated.items(Q, J.length, G.items);
    let U = Q.name("valid"), H = Q.const("len", Mq._`${z}.length`);
    J.forEach((V, O) => {
      if ((0, lQ.alwaysValidSchema)(G, V)) return;
      Q.if(Mq._`${H} > ${O}`, () => $.subschema({ keyword: W, schemaProp: O, dataProp: O }, U)), $.ok(U);
    });
    function K(V) {
      let { opts: O, errSchemaPath: N } = G, w = J.length, B = w === V.minItems && (w === V.maxItems || V[X] === false);
      if (O.strictTuples && !B) {
        let L = `"${W}" is ${w}-tuple, but minItems or maxItems/${X} are not specified or different at path "${N}"`;
        (0, lQ.checkStrictMode)(G, L, O.strictTuples);
      }
    }
  }
  Aq.validateTuple = Iq;
  Aq.default = Dk;
});
var Zq = k((Pq) => {
  Object.defineProperty(Pq, "__esModule", { value: true });
  var jk = HU(), Fk = { keyword: "prefixItems", type: "array", schemaType: ["array"], before: "uniqueItems", code: ($) => (0, jk.validateTuple)($, "items") };
  Pq.default = Fk;
});
var Sq = k((Rq) => {
  Object.defineProperty(Rq, "__esModule", { value: true });
  var Eq = a(), Ik = Q$(), Ak = Z6(), bk = UU(), Pk = { message: ({ params: { len: $ } }) => Eq.str`must NOT have more than ${$} items`, params: ({ params: { len: $ } }) => Eq._`{limit: ${$}}` }, Zk = { keyword: "items", type: "array", schemaType: ["object", "boolean"], before: "uniqueItems", error: Pk, code($) {
    let { schema: X, parentSchema: J, it: Q } = $, { prefixItems: Y } = J;
    if (Q.items = true, (0, Ik.alwaysValidSchema)(Q, X)) return;
    if (Y) (0, bk.validateAdditionalItems)($, Y);
    else $.ok((0, Ak.validateArray)($));
  } };
  Rq.default = Zk;
});
var Cq = k((vq) => {
  Object.defineProperty(vq, "__esModule", { value: true });
  var E6 = a(), cQ = Q$(), Rk = { message: ({ params: { min: $, max: X } }) => X === void 0 ? E6.str`must contain at least ${$} valid item(s)` : E6.str`must contain at least ${$} and no more than ${X} valid item(s)`, params: ({ params: { min: $, max: X } }) => X === void 0 ? E6._`{minContains: ${$}}` : E6._`{minContains: ${$}, maxContains: ${X}}` }, Sk = { keyword: "contains", type: "array", schemaType: ["object", "boolean"], before: "uniqueItems", trackErrors: true, error: Rk, code($) {
    let { gen: X, schema: J, parentSchema: Q, data: Y, it: z } = $, W, G, { minContains: U, maxContains: H } = Q;
    if (z.opts.next) W = U === void 0 ? 1 : U, G = H;
    else W = 1;
    let K = X.const("len", E6._`${Y}.length`);
    if ($.setParams({ min: W, max: G }), G === void 0 && W === 0) {
      (0, cQ.checkStrictMode)(z, '"minContains" == 0 without "maxContains": "contains" keyword ignored');
      return;
    }
    if (G !== void 0 && W > G) {
      (0, cQ.checkStrictMode)(z, '"minContains" > "maxContains" is always invalid'), $.fail();
      return;
    }
    if ((0, cQ.alwaysValidSchema)(z, J)) {
      let B = E6._`${K} >= ${W}`;
      if (G !== void 0) B = E6._`${B} && ${K} <= ${G}`;
      $.pass(B);
      return;
    }
    z.items = true;
    let V = X.name("valid");
    if (G === void 0 && W === 1) N(V, () => X.if(V, () => X.break()));
    else if (W === 0) {
      if (X.let(V, true), G !== void 0) X.if(E6._`${Y}.length > 0`, O);
    } else X.let(V, false), O();
    $.result(V, () => $.reset());
    function O() {
      let B = X.name("_valid"), L = X.let("count", 0);
      N(B, () => X.if(B, () => w(L)));
    }
    function N(B, L) {
      X.forRange("i", 0, K, (j) => {
        $.subschema({ keyword: "contains", dataProp: j, dataPropType: cQ.Type.Num, compositeRule: true }, B), L();
      });
    }
    function w(B) {
      if (X.code(E6._`${B}++`), G === void 0) X.if(E6._`${B} >= ${W}`, () => X.assign(V, true).break());
      else if (X.if(E6._`${B} > ${G}`, () => X.assign(V, false).break()), W === 1) X.assign(V, true);
      else X.if(E6._`${B} >= ${W}`, () => X.assign(V, true));
    }
  } };
  vq.default = Sk;
});
var fq = k((xq) => {
  Object.defineProperty(xq, "__esModule", { value: true });
  xq.validateSchemaDeps = xq.validatePropertyDeps = xq.error = void 0;
  var KU = a(), Ck = Q$(), l9 = Z6();
  xq.error = { message: ({ params: { property: $, depsCount: X, deps: J } }) => {
    let Q = X === 1 ? "property" : "properties";
    return KU.str`must have ${Q} ${J} when property ${$} is present`;
  }, params: ({ params: { property: $, depsCount: X, deps: J, missingProperty: Q } }) => KU._`{property: ${$},
    missingProperty: ${Q},
    depsCount: ${X},
    deps: ${J}}` };
  var kk = { keyword: "dependencies", type: "object", schemaType: "object", error: xq.error, code($) {
    let [X, J] = _k($);
    kq($, X), _q($, J);
  } };
  function _k({ schema: $ }) {
    let X = {}, J = {};
    for (let Q in $) {
      if (Q === "__proto__") continue;
      let Y = Array.isArray($[Q]) ? X : J;
      Y[Q] = $[Q];
    }
    return [X, J];
  }
  function kq($, X = $.schema) {
    let { gen: J, data: Q, it: Y } = $;
    if (Object.keys(X).length === 0) return;
    let z = J.let("missing");
    for (let W in X) {
      let G = X[W];
      if (G.length === 0) continue;
      let U = (0, l9.propertyInData)(J, Q, W, Y.opts.ownProperties);
      if ($.setParams({ property: W, depsCount: G.length, deps: G.join(", ") }), Y.allErrors) J.if(U, () => {
        for (let H of G) (0, l9.checkReportMissingProp)($, H);
      });
      else J.if(KU._`${U} && (${(0, l9.checkMissingProp)($, G, z)})`), (0, l9.reportMissingProp)($, z), J.else();
    }
  }
  xq.validatePropertyDeps = kq;
  function _q($, X = $.schema) {
    let { gen: J, data: Q, keyword: Y, it: z } = $, W = J.name("valid");
    for (let G in X) {
      if ((0, Ck.alwaysValidSchema)(z, X[G])) continue;
      J.if((0, l9.propertyInData)(J, Q, G, z.opts.ownProperties), () => {
        let U = $.subschema({ keyword: Y, schemaProp: G }, W);
        $.mergeValidEvaluated(U, W);
      }, () => J.var(W, true)), $.ok(W);
    }
  }
  xq.validateSchemaDeps = _q;
  xq.default = kk;
});
var uq = k((hq) => {
  Object.defineProperty(hq, "__esModule", { value: true });
  var gq = a(), yk = Q$(), fk = { message: "property name must be valid", params: ({ params: $ }) => gq._`{propertyName: ${$.propertyName}}` }, gk = { keyword: "propertyNames", type: "object", schemaType: ["object", "boolean"], error: fk, code($) {
    let { gen: X, schema: J, data: Q, it: Y } = $;
    if ((0, yk.alwaysValidSchema)(Y, J)) return;
    let z = X.name("valid");
    X.forIn("key", Q, (W) => {
      $.setParams({ propertyName: W }), $.subschema({ keyword: "propertyNames", data: W, dataTypes: ["string"], propertyName: W, compositeRule: true }, z), X.if((0, gq.not)(z), () => {
        if ($.error(true), !Y.allErrors) X.break();
      });
    }), $.ok(z);
  } };
  hq.default = gk;
});
var NU = k((mq) => {
  Object.defineProperty(mq, "__esModule", { value: true });
  var pQ = Z6(), g6 = a(), uk = B4(), iQ = Q$(), mk = { message: "must NOT have additional properties", params: ({ params: $ }) => g6._`{additionalProperty: ${$.additionalProperty}}` }, lk = { keyword: "additionalProperties", type: ["object"], schemaType: ["boolean", "object"], allowUndefined: true, trackErrors: true, error: mk, code($) {
    let { gen: X, schema: J, parentSchema: Q, data: Y, errsCount: z, it: W } = $;
    if (!z) throw Error("ajv implementation error");
    let { allErrors: G, opts: U } = W;
    if (W.props = true, U.removeAdditional !== "all" && (0, iQ.alwaysValidSchema)(W, J)) return;
    let H = (0, pQ.allSchemaProperties)(Q.properties), K = (0, pQ.allSchemaProperties)(Q.patternProperties);
    V(), $.ok(g6._`${z} === ${uk.default.errors}`);
    function V() {
      X.forIn("key", Y, (L) => {
        if (!H.length && !K.length) w(L);
        else X.if(O(L), () => w(L));
      });
    }
    function O(L) {
      let j;
      if (H.length > 8) {
        let I = (0, iQ.schemaRefOrVal)(W, Q.properties, "properties");
        j = (0, pQ.isOwnProperty)(X, I, L);
      } else if (H.length) j = (0, g6.or)(...H.map((I) => g6._`${L} === ${I}`));
      else j = g6.nil;
      if (K.length) j = (0, g6.or)(j, ...K.map((I) => g6._`${(0, pQ.usePattern)($, I)}.test(${L})`));
      return (0, g6.not)(j);
    }
    function N(L) {
      X.code(g6._`delete ${Y}[${L}]`);
    }
    function w(L) {
      if (U.removeAdditional === "all" || U.removeAdditional && J === false) {
        N(L);
        return;
      }
      if (J === false) {
        if ($.setParams({ additionalProperty: L }), $.error(), !G) X.break();
        return;
      }
      if (typeof J == "object" && !(0, iQ.alwaysValidSchema)(W, J)) {
        let j = X.name("valid");
        if (U.removeAdditional === "failing") B(L, j, false), X.if((0, g6.not)(j), () => {
          $.reset(), N(L);
        });
        else if (B(L, j), !G) X.if((0, g6.not)(j), () => X.break());
      }
    }
    function B(L, j, I) {
      let b = { keyword: "additionalProperties", dataProp: L, dataPropType: iQ.Type.Str };
      if (I === false) Object.assign(b, { compositeRule: true, createErrors: false, allErrors: false });
      $.subschema(b, j);
    }
  } };
  mq.default = lk;
});
var iq = k((pq) => {
  Object.defineProperty(pq, "__esModule", { value: true });
  var pk = v9(), lq = Z6(), VU = Q$(), cq = NU(), ik = { keyword: "properties", type: "object", schemaType: "object", code($) {
    let { gen: X, schema: J, parentSchema: Q, data: Y, it: z } = $;
    if (z.opts.removeAdditional === "all" && Q.additionalProperties === void 0) cq.default.code(new pk.KeywordCxt(z, cq.default, "additionalProperties"));
    let W = (0, lq.allSchemaProperties)(J);
    for (let V of W) z.definedProperties.add(V);
    if (z.opts.unevaluated && W.length && z.props !== true) z.props = VU.mergeEvaluated.props(X, (0, VU.toHash)(W), z.props);
    let G = W.filter((V) => !(0, VU.alwaysValidSchema)(z, J[V]));
    if (G.length === 0) return;
    let U = X.name("valid");
    for (let V of G) {
      if (H(V)) K(V);
      else {
        if (X.if((0, lq.propertyInData)(X, Y, V, z.opts.ownProperties)), K(V), !z.allErrors) X.else().var(U, true);
        X.endIf();
      }
      $.it.definedProperties.add(V), $.ok(U);
    }
    function H(V) {
      return z.opts.useDefaults && !z.compositeRule && J[V].default !== void 0;
    }
    function K(V) {
      $.subschema({ keyword: "properties", schemaProp: V, dataProp: V }, U);
    }
  } };
  pq.default = ik;
});
var tq = k((oq) => {
  Object.defineProperty(oq, "__esModule", { value: true });
  var nq = Z6(), nQ = a(), dq = Q$(), rq = Q$(), dk = { keyword: "patternProperties", type: "object", schemaType: "object", code($) {
    let { gen: X, schema: J, data: Q, parentSchema: Y, it: z } = $, { opts: W } = z, G = (0, nq.allSchemaProperties)(J), U = G.filter((B) => (0, dq.alwaysValidSchema)(z, J[B]));
    if (G.length === 0 || U.length === G.length && (!z.opts.unevaluated || z.props === true)) return;
    let H = W.strictSchema && !W.allowMatchingProperties && Y.properties, K = X.name("valid");
    if (z.props !== true && !(z.props instanceof nQ.Name)) z.props = (0, rq.evaluatedPropsToName)(X, z.props);
    let { props: V } = z;
    O();
    function O() {
      for (let B of G) {
        if (H) N(B);
        if (z.allErrors) w(B);
        else X.var(K, true), w(B), X.if(K);
      }
    }
    function N(B) {
      for (let L in H) if (new RegExp(B).test(L)) (0, dq.checkStrictMode)(z, `property ${L} matches pattern ${B} (use allowMatchingProperties)`);
    }
    function w(B) {
      X.forIn("key", Q, (L) => {
        X.if(nQ._`${(0, nq.usePattern)($, B)}.test(${L})`, () => {
          let j = U.includes(B);
          if (!j) $.subschema({ keyword: "patternProperties", schemaProp: B, dataProp: L, dataPropType: rq.Type.Str }, K);
          if (z.opts.unevaluated && V !== true) X.assign(nQ._`${V}[${L}]`, true);
          else if (!j && !z.allErrors) X.if((0, nQ.not)(K), () => X.break());
        });
      });
    }
  } };
  oq.default = dk;
});
var sq = k((aq) => {
  Object.defineProperty(aq, "__esModule", { value: true });
  var ok = Q$(), tk = { keyword: "not", schemaType: ["object", "boolean"], trackErrors: true, code($) {
    let { gen: X, schema: J, it: Q } = $;
    if ((0, ok.alwaysValidSchema)(Q, J)) {
      $.fail();
      return;
    }
    let Y = X.name("valid");
    $.subschema({ keyword: "not", compositeRule: true, createErrors: false, allErrors: false }, Y), $.failResult(Y, () => $.reset(), () => $.error());
  }, error: { message: "must NOT be valid" } };
  aq.default = tk;
});
var $D = k((eq) => {
  Object.defineProperty(eq, "__esModule", { value: true });
  var sk = Z6(), ek = { keyword: "anyOf", schemaType: "array", trackErrors: true, code: sk.validateUnion, error: { message: "must match a schema in anyOf" } };
  eq.default = ek;
});
var JD = k((XD) => {
  Object.defineProperty(XD, "__esModule", { value: true });
  var dQ = a(), X_ = Q$(), J_ = { message: "must match exactly one schema in oneOf", params: ({ params: $ }) => dQ._`{passingSchemas: ${$.passing}}` }, Y_ = { keyword: "oneOf", schemaType: "array", trackErrors: true, error: J_, code($) {
    let { gen: X, schema: J, parentSchema: Q, it: Y } = $;
    if (!Array.isArray(J)) throw Error("ajv implementation error");
    if (Y.opts.discriminator && Q.discriminator) return;
    let z = J, W = X.let("valid", false), G = X.let("passing", null), U = X.name("_valid");
    $.setParams({ passing: G }), X.block(H), $.result(W, () => $.reset(), () => $.error(true));
    function H() {
      z.forEach((K, V) => {
        let O;
        if ((0, X_.alwaysValidSchema)(Y, K)) X.var(U, true);
        else O = $.subschema({ keyword: "oneOf", schemaProp: V, compositeRule: true }, U);
        if (V > 0) X.if(dQ._`${U} && ${W}`).assign(W, false).assign(G, dQ._`[${G}, ${V}]`).else();
        X.if(U, () => {
          if (X.assign(W, true), X.assign(G, V), O) $.mergeEvaluated(O, dQ.Name);
        });
      });
    }
  } };
  XD.default = Y_;
});
var QD = k((YD) => {
  Object.defineProperty(YD, "__esModule", { value: true });
  var z_ = Q$(), W_ = { keyword: "allOf", schemaType: "array", code($) {
    let { gen: X, schema: J, it: Q } = $;
    if (!Array.isArray(J)) throw Error("ajv implementation error");
    let Y = X.name("valid");
    J.forEach((z, W) => {
      if ((0, z_.alwaysValidSchema)(Q, z)) return;
      let G = $.subschema({ keyword: "allOf", schemaProp: W }, Y);
      $.ok(Y), $.mergeEvaluated(G);
    });
  } };
  YD.default = W_;
});
var UD = k((GD) => {
  Object.defineProperty(GD, "__esModule", { value: true });
  var rQ = a(), WD = Q$(), U_ = { message: ({ params: $ }) => rQ.str`must match "${$.ifClause}" schema`, params: ({ params: $ }) => rQ._`{failingKeyword: ${$.ifClause}}` }, H_ = { keyword: "if", schemaType: ["object", "boolean"], trackErrors: true, error: U_, code($) {
    let { gen: X, parentSchema: J, it: Q } = $;
    if (J.then === void 0 && J.else === void 0) (0, WD.checkStrictMode)(Q, '"if" without "then" and "else" is ignored');
    let Y = zD(Q, "then"), z = zD(Q, "else");
    if (!Y && !z) return;
    let W = X.let("valid", true), G = X.name("_valid");
    if (U(), $.reset(), Y && z) {
      let K = X.let("ifClause");
      $.setParams({ ifClause: K }), X.if(G, H("then", K), H("else", K));
    } else if (Y) X.if(G, H("then"));
    else X.if((0, rQ.not)(G), H("else"));
    $.pass(W, () => $.error(true));
    function U() {
      let K = $.subschema({ keyword: "if", compositeRule: true, createErrors: false, allErrors: false }, G);
      $.mergeEvaluated(K);
    }
    function H(K, V) {
      return () => {
        let O = $.subschema({ keyword: K }, G);
        if (X.assign(W, G), $.mergeValidEvaluated(O, W), V) X.assign(V, rQ._`${K}`);
        else $.setParams({ ifClause: K });
      };
    }
  } };
  function zD($, X) {
    let J = $.schema[X];
    return J !== void 0 && !(0, WD.alwaysValidSchema)($, J);
  }
  GD.default = H_;
});
var KD = k((HD) => {
  Object.defineProperty(HD, "__esModule", { value: true });
  var N_ = Q$(), V_ = { keyword: ["then", "else"], schemaType: ["object", "boolean"], code({ keyword: $, parentSchema: X, it: J }) {
    if (X.if === void 0) (0, N_.checkStrictMode)(J, `"${$}" without "if" is ignored`);
  } };
  HD.default = V_;
});
var VD = k((ND) => {
  Object.defineProperty(ND, "__esModule", { value: true });
  var w_ = UU(), B_ = Zq(), q_ = HU(), D_ = Sq(), L_ = Cq(), j_ = fq(), F_ = uq(), M_ = NU(), I_ = iq(), A_ = tq(), b_ = sq(), P_ = $D(), Z_ = JD(), E_ = QD(), R_ = UD(), S_ = KD();
  function v_($ = false) {
    let X = [b_.default, P_.default, Z_.default, E_.default, R_.default, S_.default, F_.default, M_.default, j_.default, I_.default, A_.default];
    if ($) X.push(B_.default, D_.default);
    else X.push(w_.default, q_.default);
    return X.push(L_.default), X;
  }
  ND.default = v_;
});
var wD = k((OD) => {
  Object.defineProperty(OD, "__esModule", { value: true });
  var R$ = a(), k_ = { message: ({ schemaCode: $ }) => R$.str`must match format "${$}"`, params: ({ schemaCode: $ }) => R$._`{format: ${$}}` }, __ = { keyword: "format", type: ["number", "string"], schemaType: "string", $data: true, error: k_, code($, X) {
    let { gen: J, data: Q, $data: Y, schema: z, schemaCode: W, it: G } = $, { opts: U, errSchemaPath: H, schemaEnv: K, self: V } = G;
    if (!U.validateFormats) return;
    if (Y) O();
    else N();
    function O() {
      let w = J.scopeValue("formats", { ref: V.formats, code: U.code.formats }), B = J.const("fDef", R$._`${w}[${W}]`), L = J.let("fType"), j = J.let("format");
      J.if(R$._`typeof ${B} == "object" && !(${B} instanceof RegExp)`, () => J.assign(L, R$._`${B}.type || "string"`).assign(j, R$._`${B}.validate`), () => J.assign(L, R$._`"string"`).assign(j, B)), $.fail$data((0, R$.or)(I(), b()));
      function I() {
        if (U.strictSchema === false) return R$.nil;
        return R$._`${W} && !${j}`;
      }
      function b() {
        let x = K.$async ? R$._`(${B}.async ? await ${j}(${Q}) : ${j}(${Q}))` : R$._`${j}(${Q})`, h = R$._`(typeof ${j} == "function" ? ${x} : ${j}.test(${Q}))`;
        return R$._`${j} && ${j} !== true && ${L} === ${X} && !${h}`;
      }
    }
    function N() {
      let w = V.formats[z];
      if (!w) {
        I();
        return;
      }
      if (w === true) return;
      let [B, L, j] = b(w);
      if (B === X) $.pass(x());
      function I() {
        if (U.strictSchema === false) {
          V.logger.warn(h());
          return;
        }
        throw Error(h());
        function h() {
          return `unknown format "${z}" ignored in schema at path "${H}"`;
        }
      }
      function b(h) {
        let B$ = h instanceof RegExp ? (0, R$.regexpCode)(h) : U.code.formats ? R$._`${U.code.formats}${(0, R$.getProperty)(z)}` : void 0, x$ = J.scopeValue("formats", { key: z, ref: h, code: B$ });
        if (typeof h == "object" && !(h instanceof RegExp)) return [h.type || "string", h.validate, R$._`${x$}.validate`];
        return ["string", h, x$];
      }
      function x() {
        if (typeof w == "object" && !(w instanceof RegExp) && w.async) {
          if (!K.$async) throw Error("async format in sync schema");
          return R$._`await ${j}(${Q})`;
        }
        return typeof L == "function" ? R$._`${j}(${Q})` : R$._`${j}.test(${Q})`;
      }
    }
  } };
  OD.default = __;
});
var qD = k((BD) => {
  Object.defineProperty(BD, "__esModule", { value: true });
  var T_ = wD(), y_ = [T_.default];
  BD.default = y_;
});
var jD = k((DD) => {
  Object.defineProperty(DD, "__esModule", { value: true });
  DD.contentVocabulary = DD.metadataVocabulary = void 0;
  DD.metadataVocabulary = ["title", "description", "default", "deprecated", "readOnly", "writeOnly", "examples"];
  DD.contentVocabulary = ["contentMediaType", "contentEncoding", "contentSchema"];
});
var ID = k((MD) => {
  Object.defineProperty(MD, "__esModule", { value: true });
  var h_ = pB(), u_ = Dq(), m_ = VD(), l_ = qD(), FD = jD(), c_ = [h_.default, u_.default, (0, m_.default)(), l_.default, FD.metadataVocabulary, FD.contentVocabulary];
  MD.default = c_;
});
var ZD = k((bD) => {
  Object.defineProperty(bD, "__esModule", { value: true });
  bD.DiscrError = void 0;
  var AD;
  (function($) {
    $.Tag = "tag", $.Mapping = "mapping";
  })(AD || (bD.DiscrError = AD = {}));
});
var SD = k((RD) => {
  Object.defineProperty(RD, "__esModule", { value: true });
  var i0 = a(), OU = ZD(), ED = CQ(), i_ = C9(), n_ = Q$(), d_ = { message: ({ params: { discrError: $, tagName: X } }) => $ === OU.DiscrError.Tag ? `tag "${X}" must be string` : `value of tag "${X}" must be in oneOf`, params: ({ params: { discrError: $, tag: X, tagName: J } }) => i0._`{error: ${$}, tag: ${J}, tagValue: ${X}}` }, r_ = { keyword: "discriminator", type: "object", schemaType: "object", error: d_, code($) {
    let { gen: X, data: J, schema: Q, parentSchema: Y, it: z } = $, { oneOf: W } = Y;
    if (!z.opts.discriminator) throw Error("discriminator: requires discriminator option");
    let G = Q.propertyName;
    if (typeof G != "string") throw Error("discriminator: requires propertyName");
    if (Q.mapping) throw Error("discriminator: mapping is not supported");
    if (!W) throw Error("discriminator: requires oneOf keyword");
    let U = X.let("valid", false), H = X.const("tag", i0._`${J}${(0, i0.getProperty)(G)}`);
    X.if(i0._`typeof ${H} == "string"`, () => K(), () => $.error(false, { discrError: OU.DiscrError.Tag, tag: H, tagName: G })), $.ok(U);
    function K() {
      let N = O();
      X.if(false);
      for (let w in N) X.elseIf(i0._`${H} === ${w}`), X.assign(U, V(N[w]));
      X.else(), $.error(false, { discrError: OU.DiscrError.Mapping, tag: H, tagName: G }), X.endIf();
    }
    function V(N) {
      let w = X.name("valid"), B = $.subschema({ keyword: "oneOf", schemaProp: N }, w);
      return $.mergeEvaluated(B, i0.Name), w;
    }
    function O() {
      var N;
      let w = {}, B = j(Y), L = true;
      for (let x = 0; x < W.length; x++) {
        let h = W[x];
        if ((h === null || h === void 0 ? void 0 : h.$ref) && !(0, n_.schemaHasRulesButRef)(h, z.self.RULES)) {
          let x$ = h.$ref;
          if (h = ED.resolveRef.call(z.self, z.schemaEnv.root, z.baseId, x$), h instanceof ED.SchemaEnv) h = h.schema;
          if (h === void 0) throw new i_.default(z.opts.uriResolver, z.baseId, x$);
        }
        let B$ = (N = h === null || h === void 0 ? void 0 : h.properties) === null || N === void 0 ? void 0 : N[G];
        if (typeof B$ != "object") throw Error(`discriminator: oneOf subschemas (or referenced schemas) must have "properties/${G}"`);
        L = L && (B || j(h)), I(B$, x);
      }
      if (!L) throw Error(`discriminator: "${G}" must be required`);
      return w;
      function j({ required: x }) {
        return Array.isArray(x) && x.includes(G);
      }
      function I(x, h) {
        if (x.const) b(x.const, h);
        else if (x.enum) for (let B$ of x.enum) b(B$, h);
        else throw Error(`discriminator: "properties/${G}" must have "const" or "enum"`);
      }
      function b(x, h) {
        if (typeof x != "string" || x in w) throw Error(`discriminator: "${G}" values must be unique strings`);
        w[x] = h;
      }
    }
  } };
  RD.default = r_;
});
var vD = k((ot, t_) => {
  t_.exports = { $schema: "http://json-schema.org/draft-07/schema#", $id: "http://json-schema.org/draft-07/schema#", title: "Core schema meta-schema", definitions: { schemaArray: { type: "array", minItems: 1, items: { $ref: "#" } }, nonNegativeInteger: { type: "integer", minimum: 0 }, nonNegativeIntegerDefault0: { allOf: [{ $ref: "#/definitions/nonNegativeInteger" }, { default: 0 }] }, simpleTypes: { enum: ["array", "boolean", "integer", "null", "number", "object", "string"] }, stringArray: { type: "array", items: { type: "string" }, uniqueItems: true, default: [] } }, type: ["object", "boolean"], properties: { $id: { type: "string", format: "uri-reference" }, $schema: { type: "string", format: "uri" }, $ref: { type: "string", format: "uri-reference" }, $comment: { type: "string" }, title: { type: "string" }, description: { type: "string" }, default: true, readOnly: { type: "boolean", default: false }, examples: { type: "array", items: true }, multipleOf: { type: "number", exclusiveMinimum: 0 }, maximum: { type: "number" }, exclusiveMaximum: { type: "number" }, minimum: { type: "number" }, exclusiveMinimum: { type: "number" }, maxLength: { $ref: "#/definitions/nonNegativeInteger" }, minLength: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, pattern: { type: "string", format: "regex" }, additionalItems: { $ref: "#" }, items: { anyOf: [{ $ref: "#" }, { $ref: "#/definitions/schemaArray" }], default: true }, maxItems: { $ref: "#/definitions/nonNegativeInteger" }, minItems: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, uniqueItems: { type: "boolean", default: false }, contains: { $ref: "#" }, maxProperties: { $ref: "#/definitions/nonNegativeInteger" }, minProperties: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, required: { $ref: "#/definitions/stringArray" }, additionalProperties: { $ref: "#" }, definitions: { type: "object", additionalProperties: { $ref: "#" }, default: {} }, properties: { type: "object", additionalProperties: { $ref: "#" }, default: {} }, patternProperties: { type: "object", additionalProperties: { $ref: "#" }, propertyNames: { format: "regex" }, default: {} }, dependencies: { type: "object", additionalProperties: { anyOf: [{ $ref: "#" }, { $ref: "#/definitions/stringArray" }] } }, propertyNames: { $ref: "#" }, const: true, enum: { type: "array", items: true, minItems: 1, uniqueItems: true }, type: { anyOf: [{ $ref: "#/definitions/simpleTypes" }, { type: "array", items: { $ref: "#/definitions/simpleTypes" }, minItems: 1, uniqueItems: true }] }, format: { type: "string" }, contentMediaType: { type: "string" }, contentEncoding: { type: "string" }, if: { $ref: "#" }, then: { $ref: "#" }, else: { $ref: "#" }, allOf: { $ref: "#/definitions/schemaArray" }, anyOf: { $ref: "#/definitions/schemaArray" }, oneOf: { $ref: "#/definitions/schemaArray" }, not: { $ref: "#" } }, default: true };
});
var BU = k((W6, wU) => {
  Object.defineProperty(W6, "__esModule", { value: true });
  W6.MissingRefError = W6.ValidationError = W6.CodeGen = W6.Name = W6.nil = W6.stringify = W6.str = W6._ = W6.KeywordCxt = W6.Ajv = void 0;
  var a_ = xB(), s_ = ID(), e_ = SD(), CD = vD(), $x = ["/properties"], oQ = "http://json-schema.org/draft-07/schema";
  class c9 extends a_.default {
    _addVocabularies() {
      if (super._addVocabularies(), s_.default.forEach(($) => this.addVocabulary($)), this.opts.discriminator) this.addKeyword(e_.default);
    }
    _addDefaultMetaSchema() {
      if (super._addDefaultMetaSchema(), !this.opts.meta) return;
      let $ = this.opts.$data ? this.$dataMetaSchema(CD, $x) : CD;
      this.addMetaSchema($, oQ, false), this.refs["http://json-schema.org/schema"] = oQ;
    }
    defaultMeta() {
      return this.opts.defaultMeta = super.defaultMeta() || (this.getSchema(oQ) ? oQ : void 0);
    }
  }
  W6.Ajv = c9;
  wU.exports = W6 = c9;
  wU.exports.Ajv = c9;
  Object.defineProperty(W6, "__esModule", { value: true });
  W6.default = c9;
  var Xx = v9();
  Object.defineProperty(W6, "KeywordCxt", { enumerable: true, get: function() {
    return Xx.KeywordCxt;
  } });
  var n0 = a();
  Object.defineProperty(W6, "_", { enumerable: true, get: function() {
    return n0._;
  } });
  Object.defineProperty(W6, "str", { enumerable: true, get: function() {
    return n0.str;
  } });
  Object.defineProperty(W6, "stringify", { enumerable: true, get: function() {
    return n0.stringify;
  } });
  Object.defineProperty(W6, "nil", { enumerable: true, get: function() {
    return n0.nil;
  } });
  Object.defineProperty(W6, "Name", { enumerable: true, get: function() {
    return n0.Name;
  } });
  Object.defineProperty(W6, "CodeGen", { enumerable: true, get: function() {
    return n0.CodeGen;
  } });
  var Jx = SQ();
  Object.defineProperty(W6, "ValidationError", { enumerable: true, get: function() {
    return Jx.default;
  } });
  var Yx = C9();
  Object.defineProperty(W6, "MissingRefError", { enumerable: true, get: function() {
    return Yx.default;
  } });
});
var mD = k((hD) => {
  Object.defineProperty(hD, "__esModule", { value: true });
  hD.formatNames = hD.fastFormats = hD.fullFormats = void 0;
  function d6($, X) {
    return { validate: $, compare: X };
  }
  hD.fullFormats = { date: d6(TD, jU), time: d6(DU(true), FU), "date-time": d6(kD(true), fD), "iso-time": d6(DU(), yD), "iso-date-time": d6(kD(), gD), duration: /^P(?!$)((\d+Y)?(\d+M)?(\d+D)?(T(?=\d)(\d+H)?(\d+M)?(\d+S)?)?|(\d+W)?)$/, uri: Nx, "uri-reference": /^(?:[a-z][a-z0-9+\-.]*:)?(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'"()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\?(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i, "uri-template": /^(?:(?:[^\x00-\x20"'<>%\\^`{|}]|%[0-9a-f]{2})|\{[+#./;?&=,!@|]?(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?(?:,(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?)*\})*$/i, url: /^(?:https?|ftp):\/\/(?:\S+(?::\S*)?@)?(?:(?!(?:10|127)(?:\.\d{1,3}){3})(?!(?:169\.254|192\.168)(?:\.\d{1,3}){2})(?!172\.(?:1[6-9]|2\d|3[0-1])(?:\.\d{1,3}){2})(?:[1-9]\d?|1\d\d|2[01]\d|22[0-3])(?:\.(?:1?\d{1,2}|2[0-4]\d|25[0-5])){2}(?:\.(?:[1-9]\d?|1\d\d|2[0-4]\d|25[0-4]))|(?:(?:[a-z0-9\u{00a1}-\u{ffff}]+-)*[a-z0-9\u{00a1}-\u{ffff}]+)(?:\.(?:[a-z0-9\u{00a1}-\u{ffff}]+-)*[a-z0-9\u{00a1}-\u{ffff}]+)*(?:\.(?:[a-z\u{00a1}-\u{ffff}]{2,})))(?::\d{2,5})?(?:\/[^\s]*)?$/iu, email: /^[a-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\.[a-z0-9!#$%&'*+/=?^_`{|}~-]+)*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i, hostname: /^(?=.{1,253}\.?$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[-0-9a-z]{0,61}[0-9a-z])?)*\.?$/i, ipv4: /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/, ipv6: /^((([0-9a-f]{1,4}:){7}([0-9a-f]{1,4}|:))|(([0-9a-f]{1,4}:){6}(:[0-9a-f]{1,4}|((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){5}(((:[0-9a-f]{1,4}){1,2})|:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){4}(((:[0-9a-f]{1,4}){1,3})|((:[0-9a-f]{1,4})?:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){3}(((:[0-9a-f]{1,4}){1,4})|((:[0-9a-f]{1,4}){0,2}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){2}(((:[0-9a-f]{1,4}){1,5})|((:[0-9a-f]{1,4}){0,3}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){1}(((:[0-9a-f]{1,4}){1,6})|((:[0-9a-f]{1,4}){0,4}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(:(((:[0-9a-f]{1,4}){1,7})|((:[0-9a-f]{1,4}){0,5}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:)))$/i, regex: Lx, uuid: /^(?:urn:uuid:)?[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i, "json-pointer": /^(?:\/(?:[^~/]|~0|~1)*)*$/, "json-pointer-uri-fragment": /^#(?:\/(?:[a-z0-9_\-.!$&'()*+,;:=@]|%[0-9a-f]{2}|~0|~1)*)*$/i, "relative-json-pointer": /^(?:0|[1-9][0-9]*)(?:#|(?:\/(?:[^~/]|~0|~1)*)*)$/, byte: Vx, int32: { type: "number", validate: Bx }, int64: { type: "number", validate: qx }, float: { type: "number", validate: xD }, double: { type: "number", validate: xD }, password: true, binary: true };
  hD.fastFormats = { ...hD.fullFormats, date: d6(/^\d\d\d\d-[0-1]\d-[0-3]\d$/, jU), time: d6(/^(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)$/i, FU), "date-time": d6(/^\d\d\d\d-[0-1]\d-[0-3]\dt(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)$/i, fD), "iso-time": d6(/^(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)?$/i, yD), "iso-date-time": d6(/^\d\d\d\d-[0-1]\d-[0-3]\d[t\s](?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)?$/i, gD), uri: /^(?:[a-z][a-z0-9+\-.]*:)(?:\/?\/)?[^\s]*$/i, "uri-reference": /^(?:(?:[a-z][a-z0-9+\-.]*:)?\/?\/)?(?:[^\\\s#][^\s#]*)?(?:#[^\\\s]*)?$/i, email: /^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i };
  hD.formatNames = Object.keys(hD.fullFormats);
  function Wx($) {
    return $ % 4 === 0 && ($ % 100 !== 0 || $ % 400 === 0);
  }
  var Gx = /^(\d\d\d\d)-(\d\d)-(\d\d)$/, Ux = [0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  function TD($) {
    let X = Gx.exec($);
    if (!X) return false;
    let J = +X[1], Q = +X[2], Y = +X[3];
    return Q >= 1 && Q <= 12 && Y >= 1 && Y <= (Q === 2 && Wx(J) ? 29 : Ux[Q]);
  }
  function jU($, X) {
    if (!($ && X)) return;
    if ($ > X) return 1;
    if ($ < X) return -1;
    return 0;
  }
  var qU = /^(\d\d):(\d\d):(\d\d(?:\.\d+)?)(z|([+-])(\d\d)(?::?(\d\d))?)?$/i;
  function DU($) {
    return function(J) {
      let Q = qU.exec(J);
      if (!Q) return false;
      let Y = +Q[1], z = +Q[2], W = +Q[3], G = Q[4], U = Q[5] === "-" ? -1 : 1, H = +(Q[6] || 0), K = +(Q[7] || 0);
      if (H > 23 || K > 59 || $ && !G) return false;
      if (Y <= 23 && z <= 59 && W < 60) return true;
      let V = z - K * U, O = Y - H * U - (V < 0 ? 1 : 0);
      return (O === 23 || O === -1) && (V === 59 || V === -1) && W < 61;
    };
  }
  function FU($, X) {
    if (!($ && X)) return;
    let J = (/* @__PURE__ */ new Date("2020-01-01T" + $)).valueOf(), Q = (/* @__PURE__ */ new Date("2020-01-01T" + X)).valueOf();
    if (!(J && Q)) return;
    return J - Q;
  }
  function yD($, X) {
    if (!($ && X)) return;
    let J = qU.exec($), Q = qU.exec(X);
    if (!(J && Q)) return;
    if ($ = J[1] + J[2] + J[3], X = Q[1] + Q[2] + Q[3], $ > X) return 1;
    if ($ < X) return -1;
    return 0;
  }
  var LU = /t|\s/i;
  function kD($) {
    let X = DU($);
    return function(Q) {
      let Y = Q.split(LU);
      return Y.length === 2 && TD(Y[0]) && X(Y[1]);
    };
  }
  function fD($, X) {
    if (!($ && X)) return;
    let J = new Date($).valueOf(), Q = new Date(X).valueOf();
    if (!(J && Q)) return;
    return J - Q;
  }
  function gD($, X) {
    if (!($ && X)) return;
    let [J, Q] = $.split(LU), [Y, z] = X.split(LU), W = jU(J, Y);
    if (W === void 0) return;
    return W || FU(Q, z);
  }
  var Hx = /\/|:/, Kx = /^(?:[a-z][a-z0-9+\-.]*:)(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)(?:\?(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i;
  function Nx($) {
    return Hx.test($) && Kx.test($);
  }
  var _D = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/gm;
  function Vx($) {
    return _D.lastIndex = 0, _D.test($);
  }
  var Ox = -2147483648, wx = 2147483647;
  function Bx($) {
    return Number.isInteger($) && $ <= wx && $ >= Ox;
  }
  function qx($) {
    return Number.isInteger($);
  }
  function xD() {
    return true;
  }
  var Dx = /[^\\]\\Z/;
  function Lx($) {
    if (Dx.test($)) return false;
    try {
      return new RegExp($), true;
    } catch (X) {
      return false;
    }
  }
});
var cD = k((lD) => {
  Object.defineProperty(lD, "__esModule", { value: true });
  lD.formatLimitDefinition = void 0;
  var Fx = BU(), h6 = a(), o4 = h6.operators, tQ = { formatMaximum: { okStr: "<=", ok: o4.LTE, fail: o4.GT }, formatMinimum: { okStr: ">=", ok: o4.GTE, fail: o4.LT }, formatExclusiveMaximum: { okStr: "<", ok: o4.LT, fail: o4.GTE }, formatExclusiveMinimum: { okStr: ">", ok: o4.GT, fail: o4.LTE } }, Mx = { message: ({ keyword: $, schemaCode: X }) => h6.str`should be ${tQ[$].okStr} ${X}`, params: ({ keyword: $, schemaCode: X }) => h6._`{comparison: ${tQ[$].okStr}, limit: ${X}}` };
  lD.formatLimitDefinition = { keyword: Object.keys(tQ), type: "string", schemaType: "string", $data: true, error: Mx, code($) {
    let { gen: X, data: J, schemaCode: Q, keyword: Y, it: z } = $, { opts: W, self: G } = z;
    if (!W.validateFormats) return;
    let U = new Fx.KeywordCxt(z, G.RULES.all.format.definition, "format");
    if (U.$data) H();
    else K();
    function H() {
      let O = X.scopeValue("formats", { ref: G.formats, code: W.code.formats }), N = X.const("fmt", h6._`${O}[${U.schemaCode}]`);
      $.fail$data((0, h6.or)(h6._`typeof ${N} != "object"`, h6._`${N} instanceof RegExp`, h6._`typeof ${N}.compare != "function"`, V(N)));
    }
    function K() {
      let O = U.schema, N = G.formats[O];
      if (!N || N === true) return;
      if (typeof N != "object" || N instanceof RegExp || typeof N.compare != "function") throw Error(`"${Y}": format "${O}" does not define "compare" function`);
      let w = X.scopeValue("formats", { key: O, ref: N, code: W.code.formats ? h6._`${W.code.formats}${(0, h6.getProperty)(O)}` : void 0 });
      $.fail$data(V(w));
    }
    function V(O) {
      return h6._`${O}.compare(${J}, ${Q}) ${tQ[Y].fail} 0`;
    }
  }, dependencies: ["format"] };
  var Ix = ($) => {
    return $.addKeyword(lD.formatLimitDefinition), $;
  };
  lD.default = Ix;
});
var dD = k((p9, nD) => {
  Object.defineProperty(p9, "__esModule", { value: true });
  var d0 = mD(), bx = cD(), AU = a(), pD = new AU.Name("fullFormats"), Px = new AU.Name("fastFormats"), bU = ($, X = { keywords: true }) => {
    if (Array.isArray(X)) return iD($, X, d0.fullFormats, pD), $;
    let [J, Q] = X.mode === "fast" ? [d0.fastFormats, Px] : [d0.fullFormats, pD], Y = X.formats || d0.formatNames;
    if (iD($, Y, J, Q), X.keywords) (0, bx.default)($);
    return $;
  };
  bU.get = ($, X = "full") => {
    let Q = (X === "fast" ? d0.fastFormats : d0.fullFormats)[$];
    if (!Q) throw Error(`Unknown format "${$}"`);
    return Q;
  };
  function iD($, X, J, Q) {
    var Y, z;
    (Y = (z = $.opts.code).formats) !== null && Y !== void 0 || (z.formats = AU._`require("ajv-formats/dist/formats").${Q}`);
    for (let W of X) $.addFormat(W, J[W]);
  }
  nD.exports = p9 = bU;
  Object.defineProperty(p9, "__esModule", { value: true });
  p9.default = bU;
});
var xL = 50;
function y1($ = xL) {
  let X = new AbortController();
  return (0, import_events.setMaxListeners)($, X.signal), X;
}
var a$ = class extends Error {
};
function f1() {
  return process.versions.bun !== void 0;
}
var fL = typeof global == "object" && global && global.Object === Object && global;
var mU = fL;
var gL = typeof self == "object" && self && self.Object === Object && self;
var hL = mU || gL || Function("return this")();
var g1 = hL;
var uL = g1.Symbol;
var h1 = uL;
var lU = Object.prototype;
var mL = lU.hasOwnProperty;
var lL = lU.toString;
var e0 = h1 ? h1.toStringTag : void 0;
function cL($) {
  var X = mL.call($, e0), J = $[e0];
  try {
    $[e0] = void 0;
    var Q = true;
  } catch (z) {
  }
  var Y = lL.call($);
  if (Q) if (X) $[e0] = J;
  else delete $[e0];
  return Y;
}
var cU = cL;
var pL = Object.prototype;
var iL = pL.toString;
function nL($) {
  return iL.call($);
}
var pU = nL;
var dL = "[object Null]";
var rL = "[object Undefined]";
var iU = h1 ? h1.toStringTag : void 0;
function oL($) {
  if ($ == null) return $ === void 0 ? rL : dL;
  return iU && iU in Object($) ? cU($) : pU($);
}
var nU = oL;
function tL($) {
  var X = typeof $;
  return $ != null && (X == "object" || X == "function");
}
var o9 = tL;
var aL = "[object AsyncFunction]";
var sL = "[object Function]";
var eL = "[object GeneratorFunction]";
var $j = "[object Proxy]";
function Xj($) {
  if (!o9($)) return false;
  var X = nU($);
  return X == sL || X == eL || X == aL || X == $j;
}
var dU = Xj;
var Jj = g1["__core-js_shared__"];
var t9 = Jj;
var rU = function() {
  var $ = /[^.]+$/.exec(t9 && t9.keys && t9.keys.IE_PROTO || "");
  return $ ? "Symbol(src)_1." + $ : "";
}();
function Yj($) {
  return !!rU && rU in $;
}
var oU = Yj;
var Qj = Function.prototype;
var zj = Qj.toString;
function Wj($) {
  if ($ != null) {
    try {
      return zj.call($);
    } catch (X) {
    }
    try {
      return $ + "";
    } catch (X) {
    }
  }
  return "";
}
var tU = Wj;
var Gj = /[\\^$.*+?()[\]{}|]/g;
var Uj = /^\[object .+?Constructor\]$/;
var Hj = Function.prototype;
var Kj = Object.prototype;
var Nj = Hj.toString;
var Vj = Kj.hasOwnProperty;
var Oj = RegExp("^" + Nj.call(Vj).replace(Gj, "\\$&").replace(/hasOwnProperty|(function).*?(?=\\\()| for .+?(?=\\\])/g, "$1.*?") + "$");
function wj($) {
  if (!o9($) || oU($)) return false;
  var X = dU($) ? Oj : Uj;
  return X.test(tU($));
}
var aU = wj;
function Bj($, X) {
  return $ == null ? void 0 : $[X];
}
var sU = Bj;
function qj($, X) {
  var J = sU($, X);
  return aU(J) ? J : void 0;
}
var a9 = qj;
var Dj = a9(Object, "create");
var a6 = Dj;
function Lj() {
  this.__data__ = a6 ? a6(null) : {}, this.size = 0;
}
var eU = Lj;
function jj($) {
  var X = this.has($) && delete this.__data__[$];
  return this.size -= X ? 1 : 0, X;
}
var $H = jj;
var Fj = "__lodash_hash_undefined__";
var Mj = Object.prototype;
var Ij = Mj.hasOwnProperty;
function Aj($) {
  var X = this.__data__;
  if (a6) {
    var J = X[$];
    return J === Fj ? void 0 : J;
  }
  return Ij.call(X, $) ? X[$] : void 0;
}
var XH = Aj;
var bj = Object.prototype;
var Pj = bj.hasOwnProperty;
function Zj($) {
  var X = this.__data__;
  return a6 ? X[$] !== void 0 : Pj.call(X, $);
}
var JH = Zj;
var Ej = "__lodash_hash_undefined__";
function Rj($, X) {
  var J = this.__data__;
  return this.size += this.has($) ? 0 : 1, J[$] = a6 && X === void 0 ? Ej : X, this;
}
var YH = Rj;
function u1($) {
  var X = -1, J = $ == null ? 0 : $.length;
  this.clear();
  while (++X < J) {
    var Q = $[X];
    this.set(Q[0], Q[1]);
  }
}
u1.prototype.clear = eU;
u1.prototype.delete = $H;
u1.prototype.get = XH;
u1.prototype.has = JH;
u1.prototype.set = YH;
var eQ = u1;
function Sj() {
  this.__data__ = [], this.size = 0;
}
var QH = Sj;
function vj($, X) {
  return $ === X || $ !== $ && X !== X;
}
var zH = vj;
function Cj($, X) {
  var J = $.length;
  while (J--) if (zH($[J][0], X)) return J;
  return -1;
}
var F4 = Cj;
var kj = Array.prototype;
var _j = kj.splice;
function xj($) {
  var X = this.__data__, J = F4(X, $);
  if (J < 0) return false;
  var Q = X.length - 1;
  if (J == Q) X.pop();
  else _j.call(X, J, 1);
  return --this.size, true;
}
var WH = xj;
function Tj($) {
  var X = this.__data__, J = F4(X, $);
  return J < 0 ? void 0 : X[J][1];
}
var GH = Tj;
function yj($) {
  return F4(this.__data__, $) > -1;
}
var UH = yj;
function fj($, X) {
  var J = this.__data__, Q = F4(J, $);
  if (Q < 0) ++this.size, J.push([$, X]);
  else J[Q][1] = X;
  return this;
}
var HH = fj;
function m1($) {
  var X = -1, J = $ == null ? 0 : $.length;
  this.clear();
  while (++X < J) {
    var Q = $[X];
    this.set(Q[0], Q[1]);
  }
}
m1.prototype.clear = QH;
m1.prototype.delete = WH;
m1.prototype.get = GH;
m1.prototype.has = UH;
m1.prototype.set = HH;
var KH = m1;
var gj = a9(g1, "Map");
var NH = gj;
function hj() {
  this.size = 0, this.__data__ = { hash: new eQ(), map: new (NH || KH)(), string: new eQ() };
}
var VH = hj;
function uj($) {
  var X = typeof $;
  return X == "string" || X == "number" || X == "symbol" || X == "boolean" ? $ !== "__proto__" : $ === null;
}
var OH = uj;
function mj($, X) {
  var J = $.__data__;
  return OH(X) ? J[typeof X == "string" ? "string" : "hash"] : J.map;
}
var M4 = mj;
function lj($) {
  var X = M4(this, $).delete($);
  return this.size -= X ? 1 : 0, X;
}
var wH = lj;
function cj($) {
  return M4(this, $).get($);
}
var BH = cj;
function pj($) {
  return M4(this, $).has($);
}
var qH = pj;
function ij($, X) {
  var J = M4(this, $), Q = J.size;
  return J.set($, X), this.size += J.size == Q ? 0 : 1, this;
}
var DH = ij;
function l1($) {
  var X = -1, J = $ == null ? 0 : $.length;
  this.clear();
  while (++X < J) {
    var Q = $[X];
    this.set(Q[0], Q[1]);
  }
}
l1.prototype.clear = VH;
l1.prototype.delete = wH;
l1.prototype.get = BH;
l1.prototype.has = qH;
l1.prototype.set = DH;
var $z = l1;
var nj = "Expected a function";
function Xz($, X) {
  if (typeof $ != "function" || X != null && typeof X != "function") throw TypeError(nj);
  var J = function() {
    var Q = arguments, Y = X ? X.apply(this, Q) : Q[0], z = J.cache;
    if (z.has(Y)) return z.get(Y);
    var W = $.apply(this, Q);
    return J.cache = z.set(Y, W) || z, W;
  };
  return J.cache = new (Xz.Cache || $z)(), J;
}
Xz.Cache = $z;
var R6 = Xz;
var c1 = R6(() => {
  return (process.env.CLAUDE_CONFIG_DIR ?? (0, import_path2.join)((0, import_os.homedir)(), ".claude")).normalize("NFC");
}, () => process.env.CLAUDE_CONFIG_DIR);
function B6($) {
  if (!$) return false;
  if (typeof $ === "boolean") return $;
  let X = $.toLowerCase().trim();
  return ["1", "true", "yes", "on"].includes(X);
}
function v($, X, J, Q, Y) {
  if (Q === "m") throw TypeError("Private method is not writable");
  if (Q === "a" && !Y) throw TypeError("Private accessor was defined without a setter");
  if (typeof X === "function" ? $ !== X || !Y : !X.has($)) throw TypeError("Cannot write private member to an object whose class did not declare it");
  return Q === "a" ? Y.call($, J) : Y ? Y.value = J : X.set($, J), J;
}
function D($, X, J, Q) {
  if (J === "a" && !Q) throw TypeError("Private accessor was defined without a getter");
  if (typeof X === "function" ? $ !== X || !Q : !X.has($)) throw TypeError("Cannot read private member from an object whose class did not declare it");
  return J === "m" ? Q : J === "a" ? Q.call($) : Q ? Q.value : X.get($);
}
var Jz = function() {
  let { crypto: $ } = globalThis;
  if ($?.randomUUID) return Jz = $.randomUUID.bind($), $.randomUUID();
  let X = new Uint8Array(1), J = $ ? () => $.getRandomValues(X)[0] : () => Math.random() * 255 & 255;
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (Q) => (+Q ^ J() & 15 >> +Q / 4).toString(16));
};
function s6($) {
  return typeof $ === "object" && $ !== null && ("name" in $ && $.name === "AbortError" || "message" in $ && String($.message).includes("FetchRequestCanceledException"));
}
var $X = ($) => {
  if ($ instanceof Error) return $;
  if (typeof $ === "object" && $ !== null) {
    try {
      if (Object.prototype.toString.call($) === "[object Error]") {
        let X = Error($.message, $.cause ? { cause: $.cause } : {});
        if ($.stack) X.stack = $.stack;
        if ($.cause && !X.cause) X.cause = $.cause;
        if ($.name) X.name = $.name;
        return X;
      }
    } catch {
    }
    try {
      return Error(JSON.stringify($));
    } catch {
    }
  }
  return Error($);
};
var T = class extends Error {
};
var v$ = class _v$ extends T {
  constructor($, X, J, Q) {
    super(`${_v$.makeMessage($, X, J)}`);
    this.status = $, this.headers = Q, this.requestID = Q?.get("request-id"), this.error = X;
  }
  static makeMessage($, X, J) {
    let Q = X?.message ? typeof X.message === "string" ? X.message : JSON.stringify(X.message) : X ? JSON.stringify(X) : J;
    if ($ && Q) return `${$} ${Q}`;
    if ($) return `${$} status code (no body)`;
    if (Q) return Q;
    return "(no status code or body)";
  }
  static generate($, X, J, Q) {
    if (!$ || !Q) return new X1({ message: J, cause: $X(X) });
    let Y = X;
    if ($ === 400) return new JX($, Y, J, Q);
    if ($ === 401) return new YX($, Y, J, Q);
    if ($ === 403) return new QX($, Y, J, Q);
    if ($ === 404) return new zX($, Y, J, Q);
    if ($ === 409) return new WX($, Y, J, Q);
    if ($ === 422) return new GX($, Y, J, Q);
    if ($ === 429) return new UX($, Y, J, Q);
    if ($ >= 500) return new HX($, Y, J, Q);
    return new _v$($, Y, J, Q);
  }
};
var T$ = class extends v$ {
  constructor({ message: $ } = {}) {
    super(void 0, void 0, $ || "Request was aborted.", void 0);
  }
};
var X1 = class extends v$ {
  constructor({ message: $, cause: X }) {
    super(void 0, void 0, $ || "Connection error.", void 0);
    if (X) this.cause = X;
  }
};
var XX = class extends X1 {
  constructor({ message: $ } = {}) {
    super({ message: $ ?? "Request timed out." });
  }
};
var JX = class extends v$ {
};
var YX = class extends v$ {
};
var QX = class extends v$ {
};
var zX = class extends v$ {
};
var WX = class extends v$ {
};
var GX = class extends v$ {
};
var UX = class extends v$ {
};
var HX = class extends v$ {
};
var tj = /^[a-z][a-z0-9+.-]*:/i;
var LH = ($) => {
  return tj.test($);
};
var Yz = ($) => (Yz = Array.isArray, Yz($));
var Qz = Yz;
function s9($) {
  if (typeof $ !== "object") return {};
  return $ ?? {};
}
function zz($) {
  if (!$) return true;
  for (let X in $) return false;
  return true;
}
function jH($, X) {
  return Object.prototype.hasOwnProperty.call($, X);
}
var FH = ($, X) => {
  if (typeof X !== "number" || !Number.isInteger(X)) throw new T(`${$} must be an integer`);
  if (X < 0) throw new T(`${$} must be a positive integer`);
  return X;
};
var e9 = ($) => {
  try {
    return JSON.parse($);
  } catch (X) {
    return;
  }
};
var MH = ($) => new Promise((X) => setTimeout(X, $));
var I4 = "0.80.0";
var PH = () => {
  return typeof window < "u" && typeof window.document < "u" && typeof navigator < "u";
};
function aj() {
  if (typeof Deno < "u" && Deno.build != null) return "deno";
  if (typeof EdgeRuntime < "u") return "edge";
  if (Object.prototype.toString.call(typeof globalThis.process < "u" ? globalThis.process : 0) === "[object process]") return "node";
  return "unknown";
}
var sj = () => {
  let $ = aj();
  if ($ === "deno") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": I4, "X-Stainless-OS": AH(Deno.build.os), "X-Stainless-Arch": IH(Deno.build.arch), "X-Stainless-Runtime": "deno", "X-Stainless-Runtime-Version": typeof Deno.version === "string" ? Deno.version : Deno.version?.deno ?? "unknown" };
  if (typeof EdgeRuntime < "u") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": I4, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": `other:${EdgeRuntime}`, "X-Stainless-Runtime": "edge", "X-Stainless-Runtime-Version": globalThis.process.version };
  if ($ === "node") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": I4, "X-Stainless-OS": AH(globalThis.process.platform ?? "unknown"), "X-Stainless-Arch": IH(globalThis.process.arch ?? "unknown"), "X-Stainless-Runtime": "node", "X-Stainless-Runtime-Version": globalThis.process.version ?? "unknown" };
  let X = ej();
  if (X) return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": I4, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": "unknown", "X-Stainless-Runtime": `browser:${X.browser}`, "X-Stainless-Runtime-Version": X.version };
  return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": I4, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": "unknown", "X-Stainless-Runtime": "unknown", "X-Stainless-Runtime-Version": "unknown" };
};
function ej() {
  if (typeof navigator > "u" || !navigator) return null;
  let $ = [{ key: "edge", pattern: /Edge(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "ie", pattern: /MSIE(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "ie", pattern: /Trident(?:.*rv\:(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "chrome", pattern: /Chrome(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "firefox", pattern: /Firefox(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "safari", pattern: /(?:Version\W+(\d+)\.(\d+)(?:\.(\d+))?)?(?:\W+Mobile\S*)?\W+Safari/ }];
  for (let { key: X, pattern: J } of $) {
    let Q = J.exec(navigator.userAgent);
    if (Q) {
      let Y = Q[1] || 0, z = Q[2] || 0, W = Q[3] || 0;
      return { browser: X, version: `${Y}.${z}.${W}` };
    }
  }
  return null;
}
var IH = ($) => {
  if ($ === "x32") return "x32";
  if ($ === "x86_64" || $ === "x64") return "x64";
  if ($ === "arm") return "arm";
  if ($ === "aarch64" || $ === "arm64") return "arm64";
  if ($) return `other:${$}`;
  return "unknown";
};
var AH = ($) => {
  if ($ = $.toLowerCase(), $.includes("ios")) return "iOS";
  if ($ === "android") return "Android";
  if ($ === "darwin") return "MacOS";
  if ($ === "win32") return "Windows";
  if ($ === "freebsd") return "FreeBSD";
  if ($ === "openbsd") return "OpenBSD";
  if ($ === "linux") return "Linux";
  if ($) return `Other:${$}`;
  return "Unknown";
};
var bH;
var ZH = () => {
  return bH ?? (bH = sj());
};
function EH() {
  if (typeof fetch < "u") return fetch;
  throw Error("`fetch` is not defined as a global; Either pass `fetch` to the client, `new Anthropic({ fetch })` or polyfill the global, `globalThis.fetch = fetch`");
}
function Wz(...$) {
  let X = globalThis.ReadableStream;
  if (typeof X > "u") throw Error("`ReadableStream` is not defined as a global; You will need to polyfill it, `globalThis.ReadableStream = ReadableStream`");
  return new X(...$);
}
function $J($) {
  let X = Symbol.asyncIterator in $ ? $[Symbol.asyncIterator]() : $[Symbol.iterator]();
  return Wz({ start() {
  }, async pull(J) {
    let { done: Q, value: Y } = await X.next();
    if (Q) J.close();
    else J.enqueue(Y);
  }, async cancel() {
    await X.return?.();
  } });
}
function KX($) {
  if ($[Symbol.asyncIterator]) return $;
  let X = $.getReader();
  return { async next() {
    try {
      let J = await X.read();
      if (J?.done) X.releaseLock();
      return J;
    } catch (J) {
      throw X.releaseLock(), J;
    }
  }, async return() {
    let J = X.cancel();
    return X.releaseLock(), await J, { done: true, value: void 0 };
  }, [Symbol.asyncIterator]() {
    return this;
  } };
}
async function RH($) {
  if ($ === null || typeof $ !== "object") return;
  if ($[Symbol.asyncIterator]) {
    await $[Symbol.asyncIterator]().return?.();
    return;
  }
  let X = $.getReader(), J = X.cancel();
  X.releaseLock(), await J;
}
var SH = ({ headers: $, body: X }) => {
  return { bodyHeaders: { "content-type": "application/json" }, body: JSON.stringify(X) };
};
function vH($) {
  return Object.entries($).filter(([X, J]) => typeof J < "u").map(([X, J]) => {
    if (typeof J === "string" || typeof J === "number" || typeof J === "boolean") return `${encodeURIComponent(X)}=${encodeURIComponent(J)}`;
    if (J === null) return `${encodeURIComponent(X)}=`;
    throw new T(`Cannot stringify type ${typeof J}; Expected string, number, boolean, or null. If you need to pass nested query parameters, you can manually encode them, e.g. { query: { 'foo[key1]': value1, 'foo[key2]': value2 } }, and please open a GitHub issue requesting better support for your use case.`);
  }).join("&");
}
function _H($) {
  let X = 0;
  for (let Y of $) X += Y.length;
  let J = new Uint8Array(X), Q = 0;
  for (let Y of $) J.set(Y, Q), Q += Y.length;
  return J;
}
var CH;
function NX($) {
  let X;
  return (CH ?? (X = new globalThis.TextEncoder(), CH = X.encode.bind(X)))($);
}
var kH;
function Gz($) {
  let X;
  return (kH ?? (X = new globalThis.TextDecoder(), kH = X.decode.bind(X)))($);
}
var U6;
var H6;
var A4 = class {
  constructor() {
    U6.set(this, void 0), H6.set(this, void 0), v(this, U6, new Uint8Array(), "f"), v(this, H6, null, "f");
  }
  decode($) {
    if ($ == null) return [];
    let X = $ instanceof ArrayBuffer ? new Uint8Array($) : typeof $ === "string" ? NX($) : $;
    v(this, U6, _H([D(this, U6, "f"), X]), "f");
    let J = [], Q;
    while ((Q = JF(D(this, U6, "f"), D(this, H6, "f"))) != null) {
      if (Q.carriage && D(this, H6, "f") == null) {
        v(this, H6, Q.index, "f");
        continue;
      }
      if (D(this, H6, "f") != null && (Q.index !== D(this, H6, "f") + 1 || Q.carriage)) {
        J.push(Gz(D(this, U6, "f").subarray(0, D(this, H6, "f") - 1))), v(this, U6, D(this, U6, "f").subarray(D(this, H6, "f")), "f"), v(this, H6, null, "f");
        continue;
      }
      let Y = D(this, H6, "f") !== null ? Q.preceding - 1 : Q.preceding, z = Gz(D(this, U6, "f").subarray(0, Y));
      J.push(z), v(this, U6, D(this, U6, "f").subarray(Q.index), "f"), v(this, H6, null, "f");
    }
    return J;
  }
  flush() {
    if (!D(this, U6, "f").length) return [];
    return this.decode(`
`);
  }
};
U6 = /* @__PURE__ */ new WeakMap(), H6 = /* @__PURE__ */ new WeakMap();
A4.NEWLINE_CHARS = /* @__PURE__ */ new Set([`
`, "\r"]);
A4.NEWLINE_REGEXP = /\r\n|[\n\r]/g;
function JF($, X) {
  for (let Y = X ?? 0; Y < $.length; Y++) {
    if ($[Y] === 10) return { preceding: Y, index: Y + 1, carriage: false };
    if ($[Y] === 13) return { preceding: Y, index: Y + 1, carriage: true };
  }
  return null;
}
function xH($) {
  for (let Q = 0; Q < $.length - 1; Q++) {
    if ($[Q] === 10 && $[Q + 1] === 10) return Q + 2;
    if ($[Q] === 13 && $[Q + 1] === 13) return Q + 2;
    if ($[Q] === 13 && $[Q + 1] === 10 && Q + 3 < $.length && $[Q + 2] === 13 && $[Q + 3] === 10) return Q + 4;
  }
  return -1;
}
var JJ = { off: 0, error: 200, warn: 300, info: 400, debug: 500 };
var Uz = ($, X, J) => {
  if (!$) return;
  if (jH(JJ, $)) return $;
  _$(J).warn(`${X} was set to ${JSON.stringify($)}, expected one of ${JSON.stringify(Object.keys(JJ))}`);
  return;
};
function VX() {
}
function XJ($, X, J) {
  if (!X || JJ[$] > JJ[J]) return VX;
  else return X[$].bind(X);
}
var YF = { error: VX, warn: VX, info: VX, debug: VX };
var TH = /* @__PURE__ */ new WeakMap();
function _$($) {
  let X = $.logger, J = $.logLevel ?? "off";
  if (!X) return YF;
  let Q = TH.get(X);
  if (Q && Q[0] === J) return Q[1];
  let Y = { error: XJ("error", X, J), warn: XJ("warn", X, J), info: XJ("info", X, J), debug: XJ("debug", X, J) };
  return TH.set(X, [J, Y]), Y;
}
var e6 = ($) => {
  if ($.options) $.options = { ...$.options }, delete $.options.headers;
  if ($.headers) $.headers = Object.fromEntries(($.headers instanceof Headers ? [...$.headers] : Object.entries($.headers)).map(([X, J]) => [X, X.toLowerCase() === "x-api-key" || X.toLowerCase() === "authorization" || X.toLowerCase() === "cookie" || X.toLowerCase() === "set-cookie" ? "***" : J]));
  if ("retryOfRequestLogID" in $) {
    if ($.retryOfRequestLogID) $.retryOf = $.retryOfRequestLogID;
    delete $.retryOfRequestLogID;
  }
  return $;
};
var OX;
var K6 = class _K6 {
  constructor($, X, J) {
    this.iterator = $, OX.set(this, void 0), this.controller = X, v(this, OX, J, "f");
  }
  static fromSSEResponse($, X, J) {
    let Q = false, Y = J ? _$(J) : console;
    async function* z() {
      if (Q) throw new T("Cannot iterate over a consumed stream, use `.tee()` to split the stream.");
      Q = true;
      let W = false;
      try {
        for await (let G of QF($, X)) {
          if (G.event === "completion") try {
            yield JSON.parse(G.data);
          } catch (U) {
            throw Y.error("Could not parse message into JSON:", G.data), Y.error("From chunk:", G.raw), U;
          }
          if (G.event === "message_start" || G.event === "message_delta" || G.event === "message_stop" || G.event === "content_block_start" || G.event === "content_block_delta" || G.event === "content_block_stop") try {
            yield JSON.parse(G.data);
          } catch (U) {
            throw Y.error("Could not parse message into JSON:", G.data), Y.error("From chunk:", G.raw), U;
          }
          if (G.event === "ping") continue;
          if (G.event === "error") throw new v$(void 0, e9(G.data) ?? G.data, void 0, $.headers);
        }
        W = true;
      } catch (G) {
        if (s6(G)) return;
        throw G;
      } finally {
        if (!W) X.abort();
      }
    }
    return new _K6(z, X, J);
  }
  static fromReadableStream($, X, J) {
    let Q = false;
    async function* Y() {
      let W = new A4(), G = KX($);
      for await (let U of G) for (let H of W.decode(U)) yield H;
      for (let U of W.flush()) yield U;
    }
    async function* z() {
      if (Q) throw new T("Cannot iterate over a consumed stream, use `.tee()` to split the stream.");
      Q = true;
      let W = false;
      try {
        for await (let G of Y()) {
          if (W) continue;
          if (G) yield JSON.parse(G);
        }
        W = true;
      } catch (G) {
        if (s6(G)) return;
        throw G;
      } finally {
        if (!W) X.abort();
      }
    }
    return new _K6(z, X, J);
  }
  [(OX = /* @__PURE__ */ new WeakMap(), Symbol.asyncIterator)]() {
    return this.iterator();
  }
  tee() {
    let $ = [], X = [], J = this.iterator(), Q = (Y) => {
      return { next: () => {
        if (Y.length === 0) {
          let z = J.next();
          $.push(z), X.push(z);
        }
        return Y.shift();
      } };
    };
    return [new _K6(() => Q($), this.controller, D(this, OX, "f")), new _K6(() => Q(X), this.controller, D(this, OX, "f"))];
  }
  toReadableStream() {
    let $ = this, X;
    return Wz({ async start() {
      X = $[Symbol.asyncIterator]();
    }, async pull(J) {
      try {
        let { value: Q, done: Y } = await X.next();
        if (Y) return J.close();
        let z = NX(JSON.stringify(Q) + `
`);
        J.enqueue(z);
      } catch (Q) {
        J.error(Q);
      }
    }, async cancel() {
      await X.return?.();
    } });
  }
};
async function* QF($, X) {
  if (!$.body) {
    if (X.abort(), typeof globalThis.navigator < "u" && globalThis.navigator.product === "ReactNative") throw new T("The default react-native fetch implementation does not support streaming. Please use expo/fetch: https://docs.expo.dev/versions/latest/sdk/expo/#expofetch-api");
    throw new T("Attempted to iterate over a response with no body");
  }
  let J = new yH(), Q = new A4(), Y = KX($.body);
  for await (let z of zF(Y)) for (let W of Q.decode(z)) {
    let G = J.decode(W);
    if (G) yield G;
  }
  for (let z of Q.flush()) {
    let W = J.decode(z);
    if (W) yield W;
  }
}
async function* zF($) {
  let X = new Uint8Array();
  for await (let J of $) {
    if (J == null) continue;
    let Q = J instanceof ArrayBuffer ? new Uint8Array(J) : typeof J === "string" ? NX(J) : J, Y = new Uint8Array(X.length + Q.length);
    Y.set(X), Y.set(Q, X.length), X = Y;
    let z;
    while ((z = xH(X)) !== -1) yield X.slice(0, z), X = X.slice(z);
  }
  if (X.length > 0) yield X;
}
var yH = class {
  constructor() {
    this.event = null, this.data = [], this.chunks = [];
  }
  decode($) {
    if ($.endsWith("\r")) $ = $.substring(0, $.length - 1);
    if (!$) {
      if (!this.event && !this.data.length) return null;
      let Y = { event: this.event, data: this.data.join(`
`), raw: this.chunks };
      return this.event = null, this.data = [], this.chunks = [], Y;
    }
    if (this.chunks.push($), $.startsWith(":")) return null;
    let [X, J, Q] = WF($, ":");
    if (Q.startsWith(" ")) Q = Q.substring(1);
    if (X === "event") this.event = Q;
    else if (X === "data") this.data.push(Q);
    return null;
  }
};
function WF($, X) {
  let J = $.indexOf(X);
  if (J !== -1) return [$.substring(0, J), X, $.substring(J + X.length)];
  return [$, "", ""];
}
async function YJ($, X) {
  let { response: J, requestLogID: Q, retryOfRequestLogID: Y, startTime: z } = X, W = await (async () => {
    if (X.options.stream) {
      if (_$($).debug("response", J.status, J.url, J.headers, J.body), X.options.__streamClass) return X.options.__streamClass.fromSSEResponse(J, X.controller);
      return K6.fromSSEResponse(J, X.controller);
    }
    if (J.status === 204) return null;
    if (X.options.__binaryResponse) return J;
    let U = J.headers.get("content-type")?.split(";")[0]?.trim();
    if (U?.includes("application/json") || U?.endsWith("+json")) {
      if (J.headers.get("content-length") === "0") return;
      let O = await J.json();
      return Hz(O, J);
    }
    return await J.text();
  })();
  return _$($).debug(`[${Q}] response parsed`, e6({ retryOfRequestLogID: Y, url: J.url, status: J.status, body: W, durationMs: Date.now() - z })), W;
}
function Hz($, X) {
  if (!$ || typeof $ !== "object" || Array.isArray($)) return $;
  return Object.defineProperty($, "_request_id", { value: X.headers.get("request-id"), enumerable: false });
}
var wX;
var J1 = class _J1 extends Promise {
  constructor($, X, J = YJ) {
    super((Q) => {
      Q(null);
    });
    this.responsePromise = X, this.parseResponse = J, wX.set(this, void 0), v(this, wX, $, "f");
  }
  _thenUnwrap($) {
    return new _J1(D(this, wX, "f"), this.responsePromise, async (X, J) => Hz($(await this.parseResponse(X, J), J), J.response));
  }
  asResponse() {
    return this.responsePromise.then(($) => $.response);
  }
  async withResponse() {
    let [$, X] = await Promise.all([this.parse(), this.asResponse()]);
    return { data: $, response: X, request_id: X.headers.get("request-id") };
  }
  parse() {
    if (!this.parsedPromise) this.parsedPromise = this.responsePromise.then(($) => this.parseResponse(D(this, wX, "f"), $));
    return this.parsedPromise;
  }
  then($, X) {
    return this.parse().then($, X);
  }
  catch($) {
    return this.parse().catch($);
  }
  finally($) {
    return this.parse().finally($);
  }
};
wX = /* @__PURE__ */ new WeakMap();
var QJ;
var Kz = class {
  constructor($, X, J, Q) {
    QJ.set(this, void 0), v(this, QJ, $, "f"), this.options = Q, this.response = X, this.body = J;
  }
  hasNextPage() {
    if (!this.getPaginatedItems().length) return false;
    return this.nextPageRequestOptions() != null;
  }
  async getNextPage() {
    let $ = this.nextPageRequestOptions();
    if (!$) throw new T("No next page expected; please check `.hasNextPage()` before calling `.getNextPage()`.");
    return await D(this, QJ, "f").requestAPIList(this.constructor, $);
  }
  async *iterPages() {
    let $ = this;
    yield $;
    while ($.hasNextPage()) $ = await $.getNextPage(), yield $;
  }
  async *[(QJ = /* @__PURE__ */ new WeakMap(), Symbol.asyncIterator)]() {
    for await (let $ of this.iterPages()) for (let X of $.getPaginatedItems()) yield X;
  }
};
var zJ = class extends J1 {
  constructor($, X, J) {
    super($, X, async (Q, Y) => new J(Q, Y.response, await YJ(Q, Y), Y.options));
  }
  async *[Symbol.asyncIterator]() {
    let $ = await this;
    for await (let X of $) yield X;
  }
};
var S6 = class extends Kz {
  constructor($, X, J, Q) {
    super($, X, J, Q);
    this.data = J.data || [], this.has_more = J.has_more || false, this.first_id = J.first_id || null, this.last_id = J.last_id || null;
  }
  getPaginatedItems() {
    return this.data ?? [];
  }
  hasNextPage() {
    if (this.has_more === false) return false;
    return super.hasNextPage();
  }
  nextPageRequestOptions() {
    if (this.options.query?.before_id) {
      let X = this.first_id;
      if (!X) return null;
      return { ...this.options, query: { ...s9(this.options.query), before_id: X } };
    }
    let $ = this.last_id;
    if (!$) return null;
    return { ...this.options, query: { ...s9(this.options.query), after_id: $ } };
  }
};
var BX = class extends Kz {
  constructor($, X, J, Q) {
    super($, X, J, Q);
    this.data = J.data || [], this.has_more = J.has_more || false, this.next_page = J.next_page || null;
  }
  getPaginatedItems() {
    return this.data ?? [];
  }
  hasNextPage() {
    if (this.has_more === false) return false;
    return super.hasNextPage();
  }
  nextPageRequestOptions() {
    let $ = this.next_page;
    if (!$) return null;
    return { ...this.options, query: { ...s9(this.options.query), page: $ } };
  }
};
var Vz = () => {
  if (typeof File > "u") {
    let { process: $ } = globalThis, X = typeof $?.versions?.node === "string" && parseInt($.versions.node.split(".")) < 20;
    throw Error("`File` is not defined as a global, which is required for file uploads." + (X ? " Update to Node 20 LTS or newer, or set `globalThis.File` to `import('node:buffer').File`." : ""));
  }
};
function Y1($, X, J) {
  return Vz(), new File($, X ?? "unknown_file", J);
}
function qX($, X) {
  let J = typeof $ === "object" && $ !== null && ("name" in $ && $.name && String($.name) || "url" in $ && $.url && String($.url) || "filename" in $ && $.filename && String($.filename) || "path" in $ && $.path && String($.path)) || "";
  return X ? J.split(/[\\/]/).pop() || void 0 : J;
}
var Oz = ($) => $ != null && typeof $ === "object" && typeof $[Symbol.asyncIterator] === "function";
var p1 = async ($, X, J = true) => {
  return { ...$, body: await HF($.body, X, J) };
};
var fH = /* @__PURE__ */ new WeakMap();
function UF($) {
  let X = typeof $ === "function" ? $ : $.fetch, J = fH.get(X);
  if (J) return J;
  let Q = (async () => {
    try {
      let Y = "Response" in X ? X.Response : (await X("data:,")).constructor, z = new FormData();
      if (z.toString() === await new Y(z).text()) return false;
      return true;
    } catch {
      return true;
    }
  })();
  return fH.set(X, Q), Q;
}
var HF = async ($, X, J = true) => {
  if (!await UF(X)) throw TypeError("The provided fetch function does not support file uploads with the current global FormData class.");
  let Q = new FormData();
  return await Promise.all(Object.entries($ || {}).map(([Y, z]) => Nz(Q, Y, z, J))), Q;
};
var KF = ($) => $ instanceof Blob && "name" in $;
var Nz = async ($, X, J, Q) => {
  if (J === void 0) return;
  if (J == null) throw TypeError(`Received null for "${X}"; to pass null in FormData, you must use the string 'null'`);
  if (typeof J === "string" || typeof J === "number" || typeof J === "boolean") $.append(X, String(J));
  else if (J instanceof Response) {
    let Y = {}, z = J.headers.get("Content-Type");
    if (z) Y = { type: z };
    $.append(X, Y1([await J.blob()], qX(J, Q), Y));
  } else if (Oz(J)) $.append(X, Y1([await new Response($J(J)).blob()], qX(J, Q)));
  else if (KF(J)) $.append(X, Y1([J], qX(J, Q), { type: J.type }));
  else if (Array.isArray(J)) await Promise.all(J.map((Y) => Nz($, X + "[]", Y, Q)));
  else if (typeof J === "object") await Promise.all(Object.entries(J).map(([Y, z]) => Nz($, `${X}[${Y}]`, z, Q)));
  else throw TypeError(`Invalid value given to form, expected a string, number, boolean, object, Array, File or Blob but got ${J} instead`);
};
var gH = ($) => $ != null && typeof $ === "object" && typeof $.size === "number" && typeof $.type === "string" && typeof $.text === "function" && typeof $.slice === "function" && typeof $.arrayBuffer === "function";
var NF = ($) => $ != null && typeof $ === "object" && typeof $.name === "string" && typeof $.lastModified === "number" && gH($);
var VF = ($) => $ != null && typeof $ === "object" && typeof $.url === "string" && typeof $.blob === "function";
async function WJ($, X, J) {
  if (Vz(), $ = await $, X || (X = qX($, true)), NF($)) {
    if ($ instanceof File && X == null && J == null) return $;
    return Y1([await $.arrayBuffer()], X ?? $.name, { type: $.type, lastModified: $.lastModified, ...J });
  }
  if (VF($)) {
    let Y = await $.blob();
    return X || (X = new URL($.url).pathname.split(/[\\/]/).pop()), Y1(await wz(Y), X, J);
  }
  let Q = await wz($);
  if (!J?.type) {
    let Y = Q.find((z) => typeof z === "object" && "type" in z && z.type);
    if (typeof Y === "string") J = { ...J, type: Y };
  }
  return Y1(Q, X, J);
}
async function wz($) {
  let X = [];
  if (typeof $ === "string" || ArrayBuffer.isView($) || $ instanceof ArrayBuffer) X.push($);
  else if (gH($)) X.push($ instanceof Blob ? $ : await $.arrayBuffer());
  else if (Oz($)) for await (let J of $) X.push(...await wz(J));
  else {
    let J = $?.constructor?.name;
    throw Error(`Unexpected data type: ${typeof $}${J ? `; constructor: ${J}` : ""}${OF($)}`);
  }
  return X;
}
function OF($) {
  if (typeof $ !== "object" || $ === null) return "";
  return `; props: [${Object.getOwnPropertyNames($).map((J) => `"${J}"`).join(", ")}]`;
}
var A$ = class {
  constructor($) {
    this._client = $;
  }
};
var hH = Symbol.for("brand.privateNullableHeaders");
function* BF($) {
  if (!$) return;
  if (hH in $) {
    let { values: Q, nulls: Y } = $;
    yield* Q.entries();
    for (let z of Y) yield [z, null];
    return;
  }
  let X = false, J;
  if ($ instanceof Headers) J = $.entries();
  else if (Qz($)) J = $;
  else X = true, J = Object.entries($ ?? {});
  for (let Q of J) {
    let Y = Q[0];
    if (typeof Y !== "string") throw TypeError("expected header name to be a string");
    let z = Qz(Q[1]) ? Q[1] : [Q[1]], W = false;
    for (let G of z) {
      if (G === void 0) continue;
      if (X && !W) W = true, yield [Y, null];
      yield [Y, G];
    }
  }
}
var n = ($) => {
  let X = new Headers(), J = /* @__PURE__ */ new Set();
  for (let Q of $) {
    let Y = /* @__PURE__ */ new Set();
    for (let [z, W] of BF(Q)) {
      let G = z.toLowerCase();
      if (!Y.has(G)) X.delete(z), Y.add(G);
      if (W === null) X.delete(z), J.add(G);
      else X.append(z, W), J.delete(G);
    }
  }
  return { [hH]: true, values: X, nulls: J };
};
var DX = Symbol("anthropic.sdk.stainlessHelper");
function GJ($) {
  return typeof $ === "object" && $ !== null && DX in $;
}
function Bz($, X) {
  let J = /* @__PURE__ */ new Set();
  if ($) {
    for (let Q of $) if (GJ(Q)) J.add(Q[DX]);
  }
  if (X) for (let Q of X) {
    if (GJ(Q)) J.add(Q[DX]);
    if (Array.isArray(Q.content)) {
      for (let Y of Q.content) if (GJ(Y)) J.add(Y[DX]);
    }
  }
  return Array.from(J);
}
function UJ($, X) {
  let J = Bz($, X);
  if (J.length === 0) return {};
  return { "x-stainless-helper": J.join(", ") };
}
function uH($) {
  if (GJ($)) return { "x-stainless-helper": $[DX] };
  return {};
}
function lH($) {
  return $.replace(/[^A-Za-z0-9\-._~!$&'()*+,;=:@]+/g, encodeURIComponent);
}
var mH = Object.freeze(/* @__PURE__ */ Object.create(null));
var qF = ($ = lH) => function(J, ...Q) {
  if (J.length === 1) return J[0];
  let Y = false, z = [], W = J.reduce((K, V, O) => {
    if (/[?#]/.test(V)) Y = true;
    let N = Q[O], w = (Y ? encodeURIComponent : $)("" + N);
    if (O !== Q.length && (N == null || typeof N === "object" && N.toString === Object.getPrototypeOf(Object.getPrototypeOf(N.hasOwnProperty ?? mH) ?? mH)?.toString)) w = N + "", z.push({ start: K.length + V.length, length: w.length, error: `Value of type ${Object.prototype.toString.call(N).slice(8, -1)} is not a valid path parameter` });
    return K + V + (O === Q.length ? "" : w);
  }, ""), G = W.split(/[?#]/, 1)[0], U = /(?<=^|\/)(?:\.|%2e){1,2}(?=\/|$)/gi, H;
  while ((H = U.exec(G)) !== null) z.push({ start: H.index, length: H[0].length, error: `Value "${H[0]}" can't be safely passed as a path parameter` });
  if (z.sort((K, V) => K.start - V.start), z.length > 0) {
    let K = 0, V = z.reduce((O, N) => {
      let w = " ".repeat(N.start - K), B = "^".repeat(N.length);
      return K = N.start + N.length, O + w + B;
    }, "");
    throw new T(`Path parameters result in path with invalid segments:
${z.map((O) => O.error).join(`
`)}
${W}
${V}`);
  }
  return W;
};
var F$ = qF(lH);
var LX = class extends A$ {
  list($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.getAPIList("/v1/files", S6, { query: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "files-api-2025-04-14"].toString() }, X?.headers]) });
  }
  delete($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.delete(F$`/v1/files/${$}`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "files-api-2025-04-14"].toString() }, J?.headers]) });
  }
  download($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/files/${$}/content`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "files-api-2025-04-14"].toString(), Accept: "application/binary" }, J?.headers]), __binaryResponse: true });
  }
  retrieveMetadata($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/files/${$}`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "files-api-2025-04-14"].toString() }, J?.headers]) });
  }
  upload($, X) {
    let { betas: J, ...Q } = $;
    return this._client.post("/v1/files", p1({ body: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "files-api-2025-04-14"].toString() }, uH(Q.file), X?.headers]) }, this._client));
  }
};
var jX = class extends A$ {
  retrieve($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/models/${$}?beta=true`, { ...J, headers: n([{ ...Q?.toString() != null ? { "anthropic-beta": Q?.toString() } : void 0 }, J?.headers]) });
  }
  list($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.getAPIList("/v1/models?beta=true", S6, { query: Q, ...X, headers: n([{ ...J?.toString() != null ? { "anthropic-beta": J?.toString() } : void 0 }, X?.headers]) });
  }
};
var HJ = { "claude-opus-4-20250514": 8192, "claude-opus-4-0": 8192, "claude-4-opus-20250514": 8192, "anthropic.claude-opus-4-20250514-v1:0": 8192, "claude-opus-4@20250514": 8192, "claude-opus-4-1-20250805": 8192, "anthropic.claude-opus-4-1-20250805-v1:0": 8192, "claude-opus-4-1@20250805": 8192 };
function cH($) {
  return $?.output_format ?? $?.output_config?.format;
}
function qz($, X, J) {
  let Q = cH(X);
  if (!X || !("parse" in (Q ?? {}))) return { ...$, content: $.content.map((Y) => {
    if (Y.type === "text") {
      let z = Object.defineProperty({ ...Y }, "parsed_output", { value: null, enumerable: false });
      return Object.defineProperty(z, "parsed", { get() {
        return J.logger.warn("The `parsed` property on `text` blocks is deprecated, please use `parsed_output` instead."), null;
      }, enumerable: false });
    }
    return Y;
  }), parsed_output: null };
  return Dz($, X, J);
}
function Dz($, X, J) {
  let Q = null, Y = $.content.map((z) => {
    if (z.type === "text") {
      let W = jF(X, z.text);
      if (Q === null) Q = W;
      let G = Object.defineProperty({ ...z }, "parsed_output", { value: W, enumerable: false });
      return Object.defineProperty(G, "parsed", { get() {
        return J.logger.warn("The `parsed` property on `text` blocks is deprecated, please use `parsed_output` instead."), W;
      }, enumerable: false });
    }
    return z;
  });
  return { ...$, content: Y, parsed_output: Q };
}
function jF($, X) {
  let J = cH($);
  if (J?.type !== "json_schema") return null;
  try {
    if ("parse" in J) return J.parse(X);
    return JSON.parse(X);
  } catch (Q) {
    throw new T(`Failed to parse structured output: ${Q}`);
  }
}
var FF = ($) => {
  let X = 0, J = [];
  while (X < $.length) {
    let Q = $[X];
    if (Q === "\\") {
      X++;
      continue;
    }
    if (Q === "{") {
      J.push({ type: "brace", value: "{" }), X++;
      continue;
    }
    if (Q === "}") {
      J.push({ type: "brace", value: "}" }), X++;
      continue;
    }
    if (Q === "[") {
      J.push({ type: "paren", value: "[" }), X++;
      continue;
    }
    if (Q === "]") {
      J.push({ type: "paren", value: "]" }), X++;
      continue;
    }
    if (Q === ":") {
      J.push({ type: "separator", value: ":" }), X++;
      continue;
    }
    if (Q === ",") {
      J.push({ type: "delimiter", value: "," }), X++;
      continue;
    }
    if (Q === '"') {
      let G = "", U = false;
      Q = $[++X];
      while (Q !== '"') {
        if (X === $.length) {
          U = true;
          break;
        }
        if (Q === "\\") {
          if (X++, X === $.length) {
            U = true;
            break;
          }
          G += Q + $[X], Q = $[++X];
        } else G += Q, Q = $[++X];
      }
      if (Q = $[++X], !U) J.push({ type: "string", value: G });
      continue;
    }
    if (Q && /\s/.test(Q)) {
      X++;
      continue;
    }
    let z = /[0-9]/;
    if (Q && z.test(Q) || Q === "-" || Q === ".") {
      let G = "";
      if (Q === "-") G += Q, Q = $[++X];
      while (Q && z.test(Q) || Q === ".") G += Q, Q = $[++X];
      J.push({ type: "number", value: G });
      continue;
    }
    let W = /[a-z]/i;
    if (Q && W.test(Q)) {
      let G = "";
      while (Q && W.test(Q)) {
        if (X === $.length) break;
        G += Q, Q = $[++X];
      }
      if (G == "true" || G == "false" || G === "null") J.push({ type: "name", value: G });
      else {
        X++;
        continue;
      }
      continue;
    }
    X++;
  }
  return J;
};
var i1 = ($) => {
  if ($.length === 0) return $;
  let X = $[$.length - 1];
  switch (X.type) {
    case "separator":
      return $ = $.slice(0, $.length - 1), i1($);
      break;
    case "number":
      let J = X.value[X.value.length - 1];
      if (J === "." || J === "-") return $ = $.slice(0, $.length - 1), i1($);
    case "string":
      let Q = $[$.length - 2];
      if (Q?.type === "delimiter") return $ = $.slice(0, $.length - 1), i1($);
      else if (Q?.type === "brace" && Q.value === "{") return $ = $.slice(0, $.length - 1), i1($);
      break;
    case "delimiter":
      return $ = $.slice(0, $.length - 1), i1($);
      break;
  }
  return $;
};
var MF = ($) => {
  let X = [];
  if ($.map((J) => {
    if (J.type === "brace") if (J.value === "{") X.push("}");
    else X.splice(X.lastIndexOf("}"), 1);
    if (J.type === "paren") if (J.value === "[") X.push("]");
    else X.splice(X.lastIndexOf("]"), 1);
  }), X.length > 0) X.reverse().map((J) => {
    if (J === "}") $.push({ type: "brace", value: "}" });
    else if (J === "]") $.push({ type: "paren", value: "]" });
  });
  return $;
};
var IF = ($) => {
  let X = "";
  return $.map((J) => {
    switch (J.type) {
      case "string":
        X += '"' + J.value + '"';
        break;
      default:
        X += J.value;
        break;
    }
  }), X;
};
var KJ = ($) => JSON.parse(IF(MF(i1(FF($)))));
var q6;
var b4;
var n1;
var FX;
var NJ;
var MX;
var IX;
var VJ;
var AX;
var $4;
var bX;
var OJ;
var wJ;
var Q1;
var BJ;
var qJ;
var PX;
var Lz;
var pH;
var DJ;
var jz;
var Fz;
var Mz;
var iH;
var nH = "__json_buf";
function dH($) {
  return $.type === "tool_use" || $.type === "server_tool_use" || $.type === "mcp_tool_use";
}
var ZX = class _ZX {
  constructor($, X) {
    q6.add(this), this.messages = [], this.receivedMessages = [], b4.set(this, void 0), n1.set(this, null), this.controller = new AbortController(), FX.set(this, void 0), NJ.set(this, () => {
    }), MX.set(this, () => {
    }), IX.set(this, void 0), VJ.set(this, () => {
    }), AX.set(this, () => {
    }), $4.set(this, {}), bX.set(this, false), OJ.set(this, false), wJ.set(this, false), Q1.set(this, false), BJ.set(this, void 0), qJ.set(this, void 0), PX.set(this, void 0), DJ.set(this, (J) => {
      if (v(this, OJ, true, "f"), s6(J)) J = new T$();
      if (J instanceof T$) return v(this, wJ, true, "f"), this._emit("abort", J);
      if (J instanceof T) return this._emit("error", J);
      if (J instanceof Error) {
        let Q = new T(J.message);
        return Q.cause = J, this._emit("error", Q);
      }
      return this._emit("error", new T(String(J)));
    }), v(this, FX, new Promise((J, Q) => {
      v(this, NJ, J, "f"), v(this, MX, Q, "f");
    }), "f"), v(this, IX, new Promise((J, Q) => {
      v(this, VJ, J, "f"), v(this, AX, Q, "f");
    }), "f"), D(this, FX, "f").catch(() => {
    }), D(this, IX, "f").catch(() => {
    }), v(this, n1, $, "f"), v(this, PX, X?.logger ?? console, "f");
  }
  get response() {
    return D(this, BJ, "f");
  }
  get request_id() {
    return D(this, qJ, "f");
  }
  async withResponse() {
    v(this, Q1, true, "f");
    let $ = await D(this, FX, "f");
    if (!$) throw Error("Could not resolve a `Response` object");
    return { data: this, response: $, request_id: $.headers.get("request-id") };
  }
  static fromReadableStream($) {
    let X = new _ZX(null);
    return X._run(() => X._fromReadableStream($)), X;
  }
  static createMessage($, X, J, { logger: Q } = {}) {
    let Y = new _ZX(X, { logger: Q });
    for (let z of X.messages) Y._addMessageParam(z);
    return v(Y, n1, { ...X, stream: true }, "f"), Y._run(() => Y._createMessage($, { ...X, stream: true }, { ...J, headers: { ...J?.headers, "X-Stainless-Helper-Method": "stream" } })), Y;
  }
  _run($) {
    $().then(() => {
      this._emitFinal(), this._emit("end");
    }, D(this, DJ, "f"));
  }
  _addMessageParam($) {
    this.messages.push($);
  }
  _addMessage($, X = true) {
    if (this.receivedMessages.push($), X) this._emit("message", $);
  }
  async _createMessage($, X, J) {
    let Q = J?.signal, Y;
    if (Q) {
      if (Q.aborted) this.controller.abort();
      Y = this.controller.abort.bind(this.controller), Q.addEventListener("abort", Y);
    }
    try {
      D(this, q6, "m", jz).call(this);
      let { response: z, data: W } = await $.create({ ...X, stream: true }, { ...J, signal: this.controller.signal }).withResponse();
      this._connected(z);
      for await (let G of W) D(this, q6, "m", Fz).call(this, G);
      if (W.controller.signal?.aborted) throw new T$();
      D(this, q6, "m", Mz).call(this);
    } finally {
      if (Q && Y) Q.removeEventListener("abort", Y);
    }
  }
  _connected($) {
    if (this.ended) return;
    v(this, BJ, $, "f"), v(this, qJ, $?.headers.get("request-id"), "f"), D(this, NJ, "f").call(this, $), this._emit("connect");
  }
  get ended() {
    return D(this, bX, "f");
  }
  get errored() {
    return D(this, OJ, "f");
  }
  get aborted() {
    return D(this, wJ, "f");
  }
  abort() {
    this.controller.abort();
  }
  on($, X) {
    return (D(this, $4, "f")[$] || (D(this, $4, "f")[$] = [])).push({ listener: X }), this;
  }
  off($, X) {
    let J = D(this, $4, "f")[$];
    if (!J) return this;
    let Q = J.findIndex((Y) => Y.listener === X);
    if (Q >= 0) J.splice(Q, 1);
    return this;
  }
  once($, X) {
    return (D(this, $4, "f")[$] || (D(this, $4, "f")[$] = [])).push({ listener: X, once: true }), this;
  }
  emitted($) {
    return new Promise((X, J) => {
      if (v(this, Q1, true, "f"), $ !== "error") this.once("error", J);
      this.once($, X);
    });
  }
  async done() {
    v(this, Q1, true, "f"), await D(this, IX, "f");
  }
  get currentMessage() {
    return D(this, b4, "f");
  }
  async finalMessage() {
    return await this.done(), D(this, q6, "m", Lz).call(this);
  }
  async finalText() {
    return await this.done(), D(this, q6, "m", pH).call(this);
  }
  _emit($, ...X) {
    if (D(this, bX, "f")) return;
    if ($ === "end") v(this, bX, true, "f"), D(this, VJ, "f").call(this);
    let J = D(this, $4, "f")[$];
    if (J) D(this, $4, "f")[$] = J.filter((Q) => !Q.once), J.forEach(({ listener: Q }) => Q(...X));
    if ($ === "abort") {
      let Q = X[0];
      if (!D(this, Q1, "f") && !J?.length) Promise.reject(Q);
      D(this, MX, "f").call(this, Q), D(this, AX, "f").call(this, Q), this._emit("end");
      return;
    }
    if ($ === "error") {
      let Q = X[0];
      if (!D(this, Q1, "f") && !J?.length) Promise.reject(Q);
      D(this, MX, "f").call(this, Q), D(this, AX, "f").call(this, Q), this._emit("end");
    }
  }
  _emitFinal() {
    if (this.receivedMessages.at(-1)) this._emit("finalMessage", D(this, q6, "m", Lz).call(this));
  }
  async _fromReadableStream($, X) {
    let J = X?.signal, Q;
    if (J) {
      if (J.aborted) this.controller.abort();
      Q = this.controller.abort.bind(this.controller), J.addEventListener("abort", Q);
    }
    try {
      D(this, q6, "m", jz).call(this), this._connected(null);
      let Y = K6.fromReadableStream($, this.controller);
      for await (let z of Y) D(this, q6, "m", Fz).call(this, z);
      if (Y.controller.signal?.aborted) throw new T$();
      D(this, q6, "m", Mz).call(this);
    } finally {
      if (J && Q) J.removeEventListener("abort", Q);
    }
  }
  [(b4 = /* @__PURE__ */ new WeakMap(), n1 = /* @__PURE__ */ new WeakMap(), FX = /* @__PURE__ */ new WeakMap(), NJ = /* @__PURE__ */ new WeakMap(), MX = /* @__PURE__ */ new WeakMap(), IX = /* @__PURE__ */ new WeakMap(), VJ = /* @__PURE__ */ new WeakMap(), AX = /* @__PURE__ */ new WeakMap(), $4 = /* @__PURE__ */ new WeakMap(), bX = /* @__PURE__ */ new WeakMap(), OJ = /* @__PURE__ */ new WeakMap(), wJ = /* @__PURE__ */ new WeakMap(), Q1 = /* @__PURE__ */ new WeakMap(), BJ = /* @__PURE__ */ new WeakMap(), qJ = /* @__PURE__ */ new WeakMap(), PX = /* @__PURE__ */ new WeakMap(), DJ = /* @__PURE__ */ new WeakMap(), q6 = /* @__PURE__ */ new WeakSet(), Lz = function() {
    if (this.receivedMessages.length === 0) throw new T("stream ended without producing a Message with role=assistant");
    return this.receivedMessages.at(-1);
  }, pH = function() {
    if (this.receivedMessages.length === 0) throw new T("stream ended without producing a Message with role=assistant");
    let X = this.receivedMessages.at(-1).content.filter((J) => J.type === "text").map((J) => J.text);
    if (X.length === 0) throw new T("stream ended without producing a content block with type=text");
    return X.join(" ");
  }, jz = function() {
    if (this.ended) return;
    v(this, b4, void 0, "f");
  }, Fz = function(X) {
    if (this.ended) return;
    let J = D(this, q6, "m", iH).call(this, X);
    switch (this._emit("streamEvent", X, J), X.type) {
      case "content_block_delta": {
        let Q = J.content.at(-1);
        switch (X.delta.type) {
          case "text_delta": {
            if (Q.type === "text") this._emit("text", X.delta.text, Q.text || "");
            break;
          }
          case "citations_delta": {
            if (Q.type === "text") this._emit("citation", X.delta.citation, Q.citations ?? []);
            break;
          }
          case "input_json_delta": {
            if (dH(Q) && Q.input) this._emit("inputJson", X.delta.partial_json, Q.input);
            break;
          }
          case "thinking_delta": {
            if (Q.type === "thinking") this._emit("thinking", X.delta.thinking, Q.thinking);
            break;
          }
          case "signature_delta": {
            if (Q.type === "thinking") this._emit("signature", Q.signature);
            break;
          }
          case "compaction_delta": {
            if (Q.type === "compaction" && Q.content) this._emit("compaction", Q.content);
            break;
          }
          default:
            rH(X.delta);
        }
        break;
      }
      case "message_stop": {
        this._addMessageParam(J), this._addMessage(qz(J, D(this, n1, "f"), { logger: D(this, PX, "f") }), true);
        break;
      }
      case "content_block_stop": {
        this._emit("contentBlock", J.content.at(-1));
        break;
      }
      case "message_start": {
        v(this, b4, J, "f");
        break;
      }
      case "content_block_start":
      case "message_delta":
        break;
    }
  }, Mz = function() {
    if (this.ended) throw new T("stream has ended, this shouldn't happen");
    let X = D(this, b4, "f");
    if (!X) throw new T("request ended without sending any chunks");
    return v(this, b4, void 0, "f"), qz(X, D(this, n1, "f"), { logger: D(this, PX, "f") });
  }, iH = function(X) {
    let J = D(this, b4, "f");
    if (X.type === "message_start") {
      if (J) throw new T(`Unexpected event order, got ${X.type} before receiving "message_stop"`);
      return X.message;
    }
    if (!J) throw new T(`Unexpected event order, got ${X.type} before "message_start"`);
    switch (X.type) {
      case "message_stop":
        return J;
      case "message_delta":
        if (J.container = X.delta.container, J.stop_reason = X.delta.stop_reason, J.stop_sequence = X.delta.stop_sequence, J.usage.output_tokens = X.usage.output_tokens, J.context_management = X.context_management, X.usage.input_tokens != null) J.usage.input_tokens = X.usage.input_tokens;
        if (X.usage.cache_creation_input_tokens != null) J.usage.cache_creation_input_tokens = X.usage.cache_creation_input_tokens;
        if (X.usage.cache_read_input_tokens != null) J.usage.cache_read_input_tokens = X.usage.cache_read_input_tokens;
        if (X.usage.server_tool_use != null) J.usage.server_tool_use = X.usage.server_tool_use;
        if (X.usage.iterations != null) J.usage.iterations = X.usage.iterations;
        return J;
      case "content_block_start":
        return J.content.push(X.content_block), J;
      case "content_block_delta": {
        let Q = J.content.at(X.index);
        switch (X.delta.type) {
          case "text_delta": {
            if (Q?.type === "text") J.content[X.index] = { ...Q, text: (Q.text || "") + X.delta.text };
            break;
          }
          case "citations_delta": {
            if (Q?.type === "text") J.content[X.index] = { ...Q, citations: [...Q.citations ?? [], X.delta.citation] };
            break;
          }
          case "input_json_delta": {
            if (Q && dH(Q)) {
              let Y = Q[nH] || "";
              Y += X.delta.partial_json;
              let z = { ...Q };
              if (Object.defineProperty(z, nH, { value: Y, enumerable: false, writable: true }), Y) try {
                z.input = KJ(Y);
              } catch (W) {
                let G = new T(`Unable to parse tool parameter JSON from model. Please retry your request or adjust your prompt. Error: ${W}. JSON: ${Y}`);
                D(this, DJ, "f").call(this, G);
              }
              J.content[X.index] = z;
            }
            break;
          }
          case "thinking_delta": {
            if (Q?.type === "thinking") J.content[X.index] = { ...Q, thinking: Q.thinking + X.delta.thinking };
            break;
          }
          case "signature_delta": {
            if (Q?.type === "thinking") J.content[X.index] = { ...Q, signature: X.delta.signature };
            break;
          }
          case "compaction_delta": {
            if (Q?.type === "compaction") J.content[X.index] = { ...Q, content: (Q.content || "") + X.delta.content };
            break;
          }
          default:
            rH(X.delta);
        }
        return J;
      }
      case "content_block_stop":
        return J;
    }
  }, Symbol.asyncIterator)]() {
    let $ = [], X = [], J = false;
    return this.on("streamEvent", (Q) => {
      let Y = X.shift();
      if (Y) Y.resolve(Q);
      else $.push(Q);
    }), this.on("end", () => {
      J = true;
      for (let Q of X) Q.resolve(void 0);
      X.length = 0;
    }), this.on("abort", (Q) => {
      J = true;
      for (let Y of X) Y.reject(Q);
      X.length = 0;
    }), this.on("error", (Q) => {
      J = true;
      for (let Y of X) Y.reject(Q);
      X.length = 0;
    }), { next: async () => {
      if (!$.length) {
        if (J) return { value: void 0, done: true };
        return new Promise((Y, z) => X.push({ resolve: Y, reject: z })).then((Y) => Y ? { value: Y, done: false } : { value: void 0, done: true });
      }
      return { value: $.shift(), done: false };
    }, return: async () => {
      return this.abort(), { value: void 0, done: true };
    } };
  }
  toReadableStream() {
    return new K6(this[Symbol.asyncIterator].bind(this), this.controller).toReadableStream();
  }
};
function rH($) {
}
var d1 = class extends Error {
  constructor($) {
    let X = typeof $ === "string" ? $ : $.map((J) => {
      if (J.type === "text") return J.text;
      return `[${J.type}]`;
    }).join(" ");
    super(X);
    this.name = "ToolError", this.content = $;
  }
};
var oH = 1e5;
var tH = `You have been working on the task described above but have not yet completed it. Write a continuation summary that will allow you (or another instance of yourself) to resume work efficiently in a future context window where the conversation history will be replaced with this summary. Your summary should be structured, concise, and actionable. Include:
1. Task Overview
The user's core request and success criteria
Any clarifications or constraints they specified
2. Current State
What has been completed so far
Files created, modified, or analyzed (with paths if relevant)
Key outputs or artifacts produced
3. Important Discoveries
Technical constraints or requirements uncovered
Decisions made and their rationale
Errors encountered and how they were resolved
What approaches were tried that didn't work (and why)
4. Next Steps
Specific actions needed to complete the task
Any blockers or open questions to resolve
Priority order if multiple steps remain
5. Context to Preserve
User preferences or style requirements
Domain-specific details that aren't obvious
Any promises made to the user
Be concise but complete\u2014err on the side of including information that would prevent duplicate work or repeated mistakes. Write in a way that enables immediate resumption of the task.
Wrap your summary in <summary></summary> tags.`;
var EX;
var r1;
var z1;
var C$;
var RX;
var N6;
var X4;
var P4;
var SX;
var aH;
var Iz;
function sH() {
  let $, X;
  return { promise: new Promise((Q, Y) => {
    $ = Q, X = Y;
  }), resolve: $, reject: X };
}
var vX = class {
  constructor($, X, J) {
    EX.add(this), this.client = $, r1.set(this, false), z1.set(this, false), C$.set(this, void 0), RX.set(this, void 0), N6.set(this, void 0), X4.set(this, void 0), P4.set(this, void 0), SX.set(this, 0), v(this, C$, { params: { ...X, messages: structuredClone(X.messages) } }, "f");
    let Y = ["BetaToolRunner", ...Bz(X.tools, X.messages)].join(", ");
    v(this, RX, { ...J, headers: n([{ "x-stainless-helper": Y }, J?.headers]) }, "f"), v(this, P4, sH(), "f");
  }
  async *[(r1 = /* @__PURE__ */ new WeakMap(), z1 = /* @__PURE__ */ new WeakMap(), C$ = /* @__PURE__ */ new WeakMap(), RX = /* @__PURE__ */ new WeakMap(), N6 = /* @__PURE__ */ new WeakMap(), X4 = /* @__PURE__ */ new WeakMap(), P4 = /* @__PURE__ */ new WeakMap(), SX = /* @__PURE__ */ new WeakMap(), EX = /* @__PURE__ */ new WeakSet(), aH = async function() {
    let X = D(this, C$, "f").params.compactionControl;
    if (!X || !X.enabled) return false;
    let J = 0;
    if (D(this, N6, "f") !== void 0) try {
      let U = await D(this, N6, "f");
      J = U.usage.input_tokens + (U.usage.cache_creation_input_tokens ?? 0) + (U.usage.cache_read_input_tokens ?? 0) + U.usage.output_tokens;
    } catch {
      return false;
    }
    let Q = X.contextTokenThreshold ?? oH;
    if (J < Q) return false;
    let Y = X.model ?? D(this, C$, "f").params.model, z = X.summaryPrompt ?? tH, W = D(this, C$, "f").params.messages;
    if (W[W.length - 1].role === "assistant") {
      let U = W[W.length - 1];
      if (Array.isArray(U.content)) {
        let H = U.content.filter((K) => K.type !== "tool_use");
        if (H.length === 0) W.pop();
        else U.content = H;
      }
    }
    let G = await this.client.beta.messages.create({ model: Y, messages: [...W, { role: "user", content: [{ type: "text", text: z }] }], max_tokens: D(this, C$, "f").params.max_tokens }, { headers: { "x-stainless-helper": "compaction" } });
    if (G.content[0]?.type !== "text") throw new T("Expected text response for compaction");
    return D(this, C$, "f").params.messages = [{ role: "user", content: G.content }], true;
  }, Symbol.asyncIterator)]() {
    var $;
    if (D(this, r1, "f")) throw new T("Cannot iterate over a consumed stream");
    v(this, r1, true, "f"), v(this, z1, true, "f"), v(this, X4, void 0, "f");
    try {
      while (true) {
        let X;
        try {
          if (D(this, C$, "f").params.max_iterations && D(this, SX, "f") >= D(this, C$, "f").params.max_iterations) break;
          v(this, z1, false, "f"), v(this, X4, void 0, "f"), v(this, SX, ($ = D(this, SX, "f"), $++, $), "f"), v(this, N6, void 0, "f");
          let { max_iterations: J, compactionControl: Q, ...Y } = D(this, C$, "f").params;
          if (Y.stream) X = this.client.beta.messages.stream({ ...Y }, D(this, RX, "f")), v(this, N6, X.finalMessage(), "f"), D(this, N6, "f").catch(() => {
          }), yield X;
          else v(this, N6, this.client.beta.messages.create({ ...Y, stream: false }, D(this, RX, "f")), "f"), yield D(this, N6, "f");
          if (!await D(this, EX, "m", aH).call(this)) {
            if (!D(this, z1, "f")) {
              let { role: G, content: U } = await D(this, N6, "f");
              D(this, C$, "f").params.messages.push({ role: G, content: U });
            }
            let W = await D(this, EX, "m", Iz).call(this, D(this, C$, "f").params.messages.at(-1));
            if (W) D(this, C$, "f").params.messages.push(W);
            else if (!D(this, z1, "f")) break;
          }
        } finally {
          if (X) X.abort();
        }
      }
      if (!D(this, N6, "f")) throw new T("ToolRunner concluded without a message from the server");
      D(this, P4, "f").resolve(await D(this, N6, "f"));
    } catch (X) {
      throw v(this, r1, false, "f"), D(this, P4, "f").promise.catch(() => {
      }), D(this, P4, "f").reject(X), v(this, P4, sH(), "f"), X;
    }
  }
  setMessagesParams($) {
    if (typeof $ === "function") D(this, C$, "f").params = $(D(this, C$, "f").params);
    else D(this, C$, "f").params = $;
    v(this, z1, true, "f"), v(this, X4, void 0, "f");
  }
  async generateToolResponse() {
    let $ = await D(this, N6, "f") ?? this.params.messages.at(-1);
    if (!$) return null;
    return D(this, EX, "m", Iz).call(this, $);
  }
  done() {
    return D(this, P4, "f").promise;
  }
  async runUntilDone() {
    if (!D(this, r1, "f")) for await (let $ of this) ;
    return this.done();
  }
  get params() {
    return D(this, C$, "f").params;
  }
  pushMessages(...$) {
    this.setMessagesParams((X) => ({ ...X, messages: [...X.messages, ...$] }));
  }
  then($, X) {
    return this.runUntilDone().then($, X);
  }
};
Iz = async function(X) {
  if (D(this, X4, "f") !== void 0) return D(this, X4, "f");
  return v(this, X4, AF(D(this, C$, "f").params, X), "f"), D(this, X4, "f");
};
async function AF($, X = $.messages.at(-1)) {
  if (!X || X.role !== "assistant" || !X.content || typeof X.content === "string") return null;
  let J = X.content.filter((Y) => Y.type === "tool_use");
  if (J.length === 0) return null;
  return { role: "user", content: await Promise.all(J.map(async (Y) => {
    let z = $.tools.find((W) => ("name" in W ? W.name : W.mcp_server_name) === Y.name);
    if (!z || !("run" in z)) return { type: "tool_result", tool_use_id: Y.id, content: `Error: Tool '${Y.name}' not found`, is_error: true };
    try {
      let W = Y.input;
      if ("parse" in z && z.parse) W = z.parse(W);
      let G = await z.run(W);
      return { type: "tool_result", tool_use_id: Y.id, content: G };
    } catch (W) {
      return { type: "tool_result", tool_use_id: Y.id, content: W instanceof d1 ? W.content : `Error: ${W instanceof Error ? W.message : String(W)}`, is_error: true };
    }
  })) };
}
var o1 = class _o1 {
  constructor($, X) {
    this.iterator = $, this.controller = X;
  }
  async *decoder() {
    let $ = new A4();
    for await (let X of this.iterator) for (let J of $.decode(X)) yield JSON.parse(J);
    for (let X of $.flush()) yield JSON.parse(X);
  }
  [Symbol.asyncIterator]() {
    return this.decoder();
  }
  static fromResponse($, X) {
    if (!$.body) {
      if (X.abort(), typeof globalThis.navigator < "u" && globalThis.navigator.product === "ReactNative") throw new T("The default react-native fetch implementation does not support streaming. Please use expo/fetch: https://docs.expo.dev/versions/latest/sdk/expo/#expofetch-api");
      throw new T("Attempted to iterate over a response with no body");
    }
    return new _o1(KX($.body), X);
  }
};
var CX = class extends A$ {
  create($, X) {
    let { betas: J, ...Q } = $;
    return this._client.post("/v1/messages/batches?beta=true", { body: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "message-batches-2024-09-24"].toString() }, X?.headers]) });
  }
  retrieve($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/messages/batches/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "message-batches-2024-09-24"].toString() }, J?.headers]) });
  }
  list($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.getAPIList("/v1/messages/batches?beta=true", S6, { query: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "message-batches-2024-09-24"].toString() }, X?.headers]) });
  }
  delete($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.delete(F$`/v1/messages/batches/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "message-batches-2024-09-24"].toString() }, J?.headers]) });
  }
  cancel($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.post(F$`/v1/messages/batches/${$}/cancel?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "message-batches-2024-09-24"].toString() }, J?.headers]) });
  }
  async results($, X = {}, J) {
    let Q = await this.retrieve($);
    if (!Q.results_url) throw new T(`No batch \`results_url\`; Has it finished processing? ${Q.processing_status} - ${Q.id}`);
    let { betas: Y } = X ?? {};
    return this._client.get(Q.results_url, { ...J, headers: n([{ "anthropic-beta": [...Y ?? [], "message-batches-2024-09-24"].toString(), Accept: "application/binary" }, J?.headers]), stream: true, __binaryResponse: true })._thenUnwrap((z, W) => o1.fromResponse(W.response, W.controller));
  }
};
var eH = { "claude-1.3": "November 6th, 2024", "claude-1.3-100k": "November 6th, 2024", "claude-instant-1.1": "November 6th, 2024", "claude-instant-1.1-100k": "November 6th, 2024", "claude-instant-1.2": "November 6th, 2024", "claude-3-sonnet-20240229": "July 21st, 2025", "claude-3-opus-20240229": "January 5th, 2026", "claude-2.1": "July 21st, 2025", "claude-2.0": "July 21st, 2025", "claude-3-7-sonnet-latest": "February 19th, 2026", "claude-3-7-sonnet-20250219": "February 19th, 2026" };
var PF = ["claude-opus-4-6"];
var Z4 = class extends A$ {
  constructor() {
    super(...arguments);
    this.batches = new CX(this._client);
  }
  create($, X) {
    let J = $K($), { betas: Q, ...Y } = J;
    if (Y.model in eH) console.warn(`The model '${Y.model}' is deprecated and will reach end-of-life on ${eH[Y.model]}
Please migrate to a newer model. Visit https://docs.anthropic.com/en/docs/resources/model-deprecations for more information.`);
    if (Y.model in PF && Y.thinking && Y.thinking.type === "enabled") console.warn(`Using Claude with ${Y.model} and 'thinking.type=enabled' is deprecated. Use 'thinking.type=adaptive' instead which results in better model performance in our testing: https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`);
    let z = this._client._options.timeout;
    if (!Y.stream && z == null) {
      let G = HJ[Y.model] ?? void 0;
      z = this._client.calculateNonstreamingTimeout(Y.max_tokens, G);
    }
    let W = UJ(Y.tools, Y.messages);
    return this._client.post("/v1/messages?beta=true", { body: Y, timeout: z ?? 6e5, ...X, headers: n([{ ...Q?.toString() != null ? { "anthropic-beta": Q?.toString() } : void 0 }, W, X?.headers]), stream: J.stream ?? false });
  }
  parse($, X) {
    return X = { ...X, headers: n([{ "anthropic-beta": [...$.betas ?? [], "structured-outputs-2025-12-15"].toString() }, X?.headers]) }, this.create($, X).then((J) => Dz(J, $, { logger: this._client.logger ?? console }));
  }
  stream($, X) {
    return ZX.createMessage(this, $, X);
  }
  countTokens($, X) {
    let J = $K($), { betas: Q, ...Y } = J;
    return this._client.post("/v1/messages/count_tokens?beta=true", { body: Y, ...X, headers: n([{ "anthropic-beta": [...Q ?? [], "token-counting-2024-11-01"].toString() }, X?.headers]) });
  }
  toolRunner($, X) {
    return new vX(this._client, $, X);
  }
};
function $K($) {
  if (!$.output_format) return $;
  if ($.output_config?.format) throw new T("Both output_format and output_config.format were provided. Please use only output_config.format (output_format is deprecated).");
  let { output_format: X, ...J } = $;
  return { ...J, output_config: { ...$.output_config, format: X } };
}
Z4.Batches = CX;
Z4.BetaToolRunner = vX;
Z4.ToolError = d1;
var kX = class extends A$ {
  create($, X = {}, J) {
    let { betas: Q, ...Y } = X ?? {};
    return this._client.post(F$`/v1/skills/${$}/versions?beta=true`, p1({ body: Y, ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "skills-2025-10-02"].toString() }, J?.headers]) }, this._client));
  }
  retrieve($, X, J) {
    let { skill_id: Q, betas: Y } = X;
    return this._client.get(F$`/v1/skills/${Q}/versions/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Y ?? [], "skills-2025-10-02"].toString() }, J?.headers]) });
  }
  list($, X = {}, J) {
    let { betas: Q, ...Y } = X ?? {};
    return this._client.getAPIList(F$`/v1/skills/${$}/versions?beta=true`, BX, { query: Y, ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "skills-2025-10-02"].toString() }, J?.headers]) });
  }
  delete($, X, J) {
    let { skill_id: Q, betas: Y } = X;
    return this._client.delete(F$`/v1/skills/${Q}/versions/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Y ?? [], "skills-2025-10-02"].toString() }, J?.headers]) });
  }
};
var t1 = class extends A$ {
  constructor() {
    super(...arguments);
    this.versions = new kX(this._client);
  }
  create($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.post("/v1/skills?beta=true", p1({ body: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "skills-2025-10-02"].toString() }, X?.headers]) }, this._client, false));
  }
  retrieve($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/skills/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "skills-2025-10-02"].toString() }, J?.headers]) });
  }
  list($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.getAPIList("/v1/skills?beta=true", BX, { query: Q, ...X, headers: n([{ "anthropic-beta": [...J ?? [], "skills-2025-10-02"].toString() }, X?.headers]) });
  }
  delete($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.delete(F$`/v1/skills/${$}?beta=true`, { ...J, headers: n([{ "anthropic-beta": [...Q ?? [], "skills-2025-10-02"].toString() }, J?.headers]) });
  }
};
t1.Versions = kX;
var m6 = class extends A$ {
  constructor() {
    super(...arguments);
    this.models = new jX(this._client), this.messages = new Z4(this._client), this.files = new LX(this._client), this.skills = new t1(this._client);
  }
};
m6.Models = jX;
m6.Messages = Z4;
m6.Files = LX;
m6.Skills = t1;
var a1 = class extends A$ {
  create($, X) {
    let { betas: J, ...Q } = $;
    return this._client.post("/v1/complete", { body: Q, timeout: this._client._options.timeout ?? 6e5, ...X, headers: n([{ ...J?.toString() != null ? { "anthropic-beta": J?.toString() } : void 0 }, X?.headers]), stream: $.stream ?? false });
  }
};
function XK($) {
  return $?.output_config?.format;
}
function Az($, X, J) {
  let Q = XK(X);
  if (!X || !("parse" in (Q ?? {}))) return { ...$, content: $.content.map((Y) => {
    if (Y.type === "text") return Object.defineProperty({ ...Y }, "parsed_output", { value: null, enumerable: false });
    return Y;
  }), parsed_output: null };
  return bz($, X, J);
}
function bz($, X, J) {
  let Q = null, Y = $.content.map((z) => {
    if (z.type === "text") {
      let W = SF(X, z.text);
      if (Q === null) Q = W;
      return Object.defineProperty({ ...z }, "parsed_output", { value: W, enumerable: false });
    }
    return z;
  });
  return { ...$, content: Y, parsed_output: Q };
}
function SF($, X) {
  let J = XK($);
  if (J?.type !== "json_schema") return null;
  try {
    if ("parse" in J) return J.parse(X);
    return JSON.parse(X);
  } catch (Q) {
    throw new T(`Failed to parse structured output: ${Q}`);
  }
}
var D6;
var E4;
var s1;
var _X;
var LJ;
var xX;
var TX;
var jJ;
var yX;
var J4;
var fX;
var FJ;
var MJ;
var W1;
var IJ;
var AJ;
var gX;
var Pz;
var JK;
var Zz;
var Ez;
var Rz;
var Sz;
var YK;
var QK = "__json_buf";
function zK($) {
  return $.type === "tool_use" || $.type === "server_tool_use";
}
var hX = class _hX {
  constructor($, X) {
    D6.add(this), this.messages = [], this.receivedMessages = [], E4.set(this, void 0), s1.set(this, null), this.controller = new AbortController(), _X.set(this, void 0), LJ.set(this, () => {
    }), xX.set(this, () => {
    }), TX.set(this, void 0), jJ.set(this, () => {
    }), yX.set(this, () => {
    }), J4.set(this, {}), fX.set(this, false), FJ.set(this, false), MJ.set(this, false), W1.set(this, false), IJ.set(this, void 0), AJ.set(this, void 0), gX.set(this, void 0), Zz.set(this, (J) => {
      if (v(this, FJ, true, "f"), s6(J)) J = new T$();
      if (J instanceof T$) return v(this, MJ, true, "f"), this._emit("abort", J);
      if (J instanceof T) return this._emit("error", J);
      if (J instanceof Error) {
        let Q = new T(J.message);
        return Q.cause = J, this._emit("error", Q);
      }
      return this._emit("error", new T(String(J)));
    }), v(this, _X, new Promise((J, Q) => {
      v(this, LJ, J, "f"), v(this, xX, Q, "f");
    }), "f"), v(this, TX, new Promise((J, Q) => {
      v(this, jJ, J, "f"), v(this, yX, Q, "f");
    }), "f"), D(this, _X, "f").catch(() => {
    }), D(this, TX, "f").catch(() => {
    }), v(this, s1, $, "f"), v(this, gX, X?.logger ?? console, "f");
  }
  get response() {
    return D(this, IJ, "f");
  }
  get request_id() {
    return D(this, AJ, "f");
  }
  async withResponse() {
    v(this, W1, true, "f");
    let $ = await D(this, _X, "f");
    if (!$) throw Error("Could not resolve a `Response` object");
    return { data: this, response: $, request_id: $.headers.get("request-id") };
  }
  static fromReadableStream($) {
    let X = new _hX(null);
    return X._run(() => X._fromReadableStream($)), X;
  }
  static createMessage($, X, J, { logger: Q } = {}) {
    let Y = new _hX(X, { logger: Q });
    for (let z of X.messages) Y._addMessageParam(z);
    return v(Y, s1, { ...X, stream: true }, "f"), Y._run(() => Y._createMessage($, { ...X, stream: true }, { ...J, headers: { ...J?.headers, "X-Stainless-Helper-Method": "stream" } })), Y;
  }
  _run($) {
    $().then(() => {
      this._emitFinal(), this._emit("end");
    }, D(this, Zz, "f"));
  }
  _addMessageParam($) {
    this.messages.push($);
  }
  _addMessage($, X = true) {
    if (this.receivedMessages.push($), X) this._emit("message", $);
  }
  async _createMessage($, X, J) {
    let Q = J?.signal, Y;
    if (Q) {
      if (Q.aborted) this.controller.abort();
      Y = this.controller.abort.bind(this.controller), Q.addEventListener("abort", Y);
    }
    try {
      D(this, D6, "m", Ez).call(this);
      let { response: z, data: W } = await $.create({ ...X, stream: true }, { ...J, signal: this.controller.signal }).withResponse();
      this._connected(z);
      for await (let G of W) D(this, D6, "m", Rz).call(this, G);
      if (W.controller.signal?.aborted) throw new T$();
      D(this, D6, "m", Sz).call(this);
    } finally {
      if (Q && Y) Q.removeEventListener("abort", Y);
    }
  }
  _connected($) {
    if (this.ended) return;
    v(this, IJ, $, "f"), v(this, AJ, $?.headers.get("request-id"), "f"), D(this, LJ, "f").call(this, $), this._emit("connect");
  }
  get ended() {
    return D(this, fX, "f");
  }
  get errored() {
    return D(this, FJ, "f");
  }
  get aborted() {
    return D(this, MJ, "f");
  }
  abort() {
    this.controller.abort();
  }
  on($, X) {
    return (D(this, J4, "f")[$] || (D(this, J4, "f")[$] = [])).push({ listener: X }), this;
  }
  off($, X) {
    let J = D(this, J4, "f")[$];
    if (!J) return this;
    let Q = J.findIndex((Y) => Y.listener === X);
    if (Q >= 0) J.splice(Q, 1);
    return this;
  }
  once($, X) {
    return (D(this, J4, "f")[$] || (D(this, J4, "f")[$] = [])).push({ listener: X, once: true }), this;
  }
  emitted($) {
    return new Promise((X, J) => {
      if (v(this, W1, true, "f"), $ !== "error") this.once("error", J);
      this.once($, X);
    });
  }
  async done() {
    v(this, W1, true, "f"), await D(this, TX, "f");
  }
  get currentMessage() {
    return D(this, E4, "f");
  }
  async finalMessage() {
    return await this.done(), D(this, D6, "m", Pz).call(this);
  }
  async finalText() {
    return await this.done(), D(this, D6, "m", JK).call(this);
  }
  _emit($, ...X) {
    if (D(this, fX, "f")) return;
    if ($ === "end") v(this, fX, true, "f"), D(this, jJ, "f").call(this);
    let J = D(this, J4, "f")[$];
    if (J) D(this, J4, "f")[$] = J.filter((Q) => !Q.once), J.forEach(({ listener: Q }) => Q(...X));
    if ($ === "abort") {
      let Q = X[0];
      if (!D(this, W1, "f") && !J?.length) Promise.reject(Q);
      D(this, xX, "f").call(this, Q), D(this, yX, "f").call(this, Q), this._emit("end");
      return;
    }
    if ($ === "error") {
      let Q = X[0];
      if (!D(this, W1, "f") && !J?.length) Promise.reject(Q);
      D(this, xX, "f").call(this, Q), D(this, yX, "f").call(this, Q), this._emit("end");
    }
  }
  _emitFinal() {
    if (this.receivedMessages.at(-1)) this._emit("finalMessage", D(this, D6, "m", Pz).call(this));
  }
  async _fromReadableStream($, X) {
    let J = X?.signal, Q;
    if (J) {
      if (J.aborted) this.controller.abort();
      Q = this.controller.abort.bind(this.controller), J.addEventListener("abort", Q);
    }
    try {
      D(this, D6, "m", Ez).call(this), this._connected(null);
      let Y = K6.fromReadableStream($, this.controller);
      for await (let z of Y) D(this, D6, "m", Rz).call(this, z);
      if (Y.controller.signal?.aborted) throw new T$();
      D(this, D6, "m", Sz).call(this);
    } finally {
      if (J && Q) J.removeEventListener("abort", Q);
    }
  }
  [(E4 = /* @__PURE__ */ new WeakMap(), s1 = /* @__PURE__ */ new WeakMap(), _X = /* @__PURE__ */ new WeakMap(), LJ = /* @__PURE__ */ new WeakMap(), xX = /* @__PURE__ */ new WeakMap(), TX = /* @__PURE__ */ new WeakMap(), jJ = /* @__PURE__ */ new WeakMap(), yX = /* @__PURE__ */ new WeakMap(), J4 = /* @__PURE__ */ new WeakMap(), fX = /* @__PURE__ */ new WeakMap(), FJ = /* @__PURE__ */ new WeakMap(), MJ = /* @__PURE__ */ new WeakMap(), W1 = /* @__PURE__ */ new WeakMap(), IJ = /* @__PURE__ */ new WeakMap(), AJ = /* @__PURE__ */ new WeakMap(), gX = /* @__PURE__ */ new WeakMap(), Zz = /* @__PURE__ */ new WeakMap(), D6 = /* @__PURE__ */ new WeakSet(), Pz = function() {
    if (this.receivedMessages.length === 0) throw new T("stream ended without producing a Message with role=assistant");
    return this.receivedMessages.at(-1);
  }, JK = function() {
    if (this.receivedMessages.length === 0) throw new T("stream ended without producing a Message with role=assistant");
    let X = this.receivedMessages.at(-1).content.filter((J) => J.type === "text").map((J) => J.text);
    if (X.length === 0) throw new T("stream ended without producing a content block with type=text");
    return X.join(" ");
  }, Ez = function() {
    if (this.ended) return;
    v(this, E4, void 0, "f");
  }, Rz = function(X) {
    if (this.ended) return;
    let J = D(this, D6, "m", YK).call(this, X);
    switch (this._emit("streamEvent", X, J), X.type) {
      case "content_block_delta": {
        let Q = J.content.at(-1);
        switch (X.delta.type) {
          case "text_delta": {
            if (Q.type === "text") this._emit("text", X.delta.text, Q.text || "");
            break;
          }
          case "citations_delta": {
            if (Q.type === "text") this._emit("citation", X.delta.citation, Q.citations ?? []);
            break;
          }
          case "input_json_delta": {
            if (zK(Q) && Q.input) this._emit("inputJson", X.delta.partial_json, Q.input);
            break;
          }
          case "thinking_delta": {
            if (Q.type === "thinking") this._emit("thinking", X.delta.thinking, Q.thinking);
            break;
          }
          case "signature_delta": {
            if (Q.type === "thinking") this._emit("signature", Q.signature);
            break;
          }
          default:
            WK(X.delta);
        }
        break;
      }
      case "message_stop": {
        this._addMessageParam(J), this._addMessage(Az(J, D(this, s1, "f"), { logger: D(this, gX, "f") }), true);
        break;
      }
      case "content_block_stop": {
        this._emit("contentBlock", J.content.at(-1));
        break;
      }
      case "message_start": {
        v(this, E4, J, "f");
        break;
      }
      case "content_block_start":
      case "message_delta":
        break;
    }
  }, Sz = function() {
    if (this.ended) throw new T("stream has ended, this shouldn't happen");
    let X = D(this, E4, "f");
    if (!X) throw new T("request ended without sending any chunks");
    return v(this, E4, void 0, "f"), Az(X, D(this, s1, "f"), { logger: D(this, gX, "f") });
  }, YK = function(X) {
    let J = D(this, E4, "f");
    if (X.type === "message_start") {
      if (J) throw new T(`Unexpected event order, got ${X.type} before receiving "message_stop"`);
      return X.message;
    }
    if (!J) throw new T(`Unexpected event order, got ${X.type} before "message_start"`);
    switch (X.type) {
      case "message_stop":
        return J;
      case "message_delta":
        if (J.stop_reason = X.delta.stop_reason, J.stop_sequence = X.delta.stop_sequence, J.usage.output_tokens = X.usage.output_tokens, X.usage.input_tokens != null) J.usage.input_tokens = X.usage.input_tokens;
        if (X.usage.cache_creation_input_tokens != null) J.usage.cache_creation_input_tokens = X.usage.cache_creation_input_tokens;
        if (X.usage.cache_read_input_tokens != null) J.usage.cache_read_input_tokens = X.usage.cache_read_input_tokens;
        if (X.usage.server_tool_use != null) J.usage.server_tool_use = X.usage.server_tool_use;
        return J;
      case "content_block_start":
        return J.content.push({ ...X.content_block }), J;
      case "content_block_delta": {
        let Q = J.content.at(X.index);
        switch (X.delta.type) {
          case "text_delta": {
            if (Q?.type === "text") J.content[X.index] = { ...Q, text: (Q.text || "") + X.delta.text };
            break;
          }
          case "citations_delta": {
            if (Q?.type === "text") J.content[X.index] = { ...Q, citations: [...Q.citations ?? [], X.delta.citation] };
            break;
          }
          case "input_json_delta": {
            if (Q && zK(Q)) {
              let Y = Q[QK] || "";
              Y += X.delta.partial_json;
              let z = { ...Q };
              if (Object.defineProperty(z, QK, { value: Y, enumerable: false, writable: true }), Y) z.input = KJ(Y);
              J.content[X.index] = z;
            }
            break;
          }
          case "thinking_delta": {
            if (Q?.type === "thinking") J.content[X.index] = { ...Q, thinking: Q.thinking + X.delta.thinking };
            break;
          }
          case "signature_delta": {
            if (Q?.type === "thinking") J.content[X.index] = { ...Q, signature: X.delta.signature };
            break;
          }
          default:
            WK(X.delta);
        }
        return J;
      }
      case "content_block_stop":
        return J;
    }
  }, Symbol.asyncIterator)]() {
    let $ = [], X = [], J = false;
    return this.on("streamEvent", (Q) => {
      let Y = X.shift();
      if (Y) Y.resolve(Q);
      else $.push(Q);
    }), this.on("end", () => {
      J = true;
      for (let Q of X) Q.resolve(void 0);
      X.length = 0;
    }), this.on("abort", (Q) => {
      J = true;
      for (let Y of X) Y.reject(Q);
      X.length = 0;
    }), this.on("error", (Q) => {
      J = true;
      for (let Y of X) Y.reject(Q);
      X.length = 0;
    }), { next: async () => {
      if (!$.length) {
        if (J) return { value: void 0, done: true };
        return new Promise((Y, z) => X.push({ resolve: Y, reject: z })).then((Y) => Y ? { value: Y, done: false } : { value: void 0, done: true });
      }
      return { value: $.shift(), done: false };
    }, return: async () => {
      return this.abort(), { value: void 0, done: true };
    } };
  }
  toReadableStream() {
    return new K6(this[Symbol.asyncIterator].bind(this), this.controller).toReadableStream();
  }
};
function WK($) {
}
var uX = class extends A$ {
  create($, X) {
    return this._client.post("/v1/messages/batches", { body: $, ...X });
  }
  retrieve($, X) {
    return this._client.get(F$`/v1/messages/batches/${$}`, X);
  }
  list($ = {}, X) {
    return this._client.getAPIList("/v1/messages/batches", S6, { query: $, ...X });
  }
  delete($, X) {
    return this._client.delete(F$`/v1/messages/batches/${$}`, X);
  }
  cancel($, X) {
    return this._client.post(F$`/v1/messages/batches/${$}/cancel`, X);
  }
  async results($, X) {
    let J = await this.retrieve($);
    if (!J.results_url) throw new T(`No batch \`results_url\`; Has it finished processing? ${J.processing_status} - ${J.id}`);
    return this._client.get(J.results_url, { ...X, headers: n([{ Accept: "application/binary" }, X?.headers]), stream: true, __binaryResponse: true })._thenUnwrap((Q, Y) => o1.fromResponse(Y.response, Y.controller));
  }
};
var G1 = class extends A$ {
  constructor() {
    super(...arguments);
    this.batches = new uX(this._client);
  }
  create($, X) {
    if ($.model in GK) console.warn(`The model '${$.model}' is deprecated and will reach end-of-life on ${GK[$.model]}
Please migrate to a newer model. Visit https://docs.anthropic.com/en/docs/resources/model-deprecations for more information.`);
    if ($.model in CF && $.thinking && $.thinking.type === "enabled") console.warn(`Using Claude with ${$.model} and 'thinking.type=enabled' is deprecated. Use 'thinking.type=adaptive' instead which results in better model performance in our testing: https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`);
    let J = this._client._options.timeout;
    if (!$.stream && J == null) {
      let Y = HJ[$.model] ?? void 0;
      J = this._client.calculateNonstreamingTimeout($.max_tokens, Y);
    }
    let Q = UJ($.tools, $.messages);
    return this._client.post("/v1/messages", { body: $, timeout: J ?? 6e5, ...X, headers: n([Q, X?.headers]), stream: $.stream ?? false });
  }
  parse($, X) {
    return this.create($, X).then((J) => bz(J, $, { logger: this._client.logger ?? console }));
  }
  stream($, X) {
    return hX.createMessage(this, $, X, { logger: this._client.logger ?? console });
  }
  countTokens($, X) {
    return this._client.post("/v1/messages/count_tokens", { body: $, ...X });
  }
};
var GK = { "claude-1.3": "November 6th, 2024", "claude-1.3-100k": "November 6th, 2024", "claude-instant-1.1": "November 6th, 2024", "claude-instant-1.1-100k": "November 6th, 2024", "claude-instant-1.2": "November 6th, 2024", "claude-3-sonnet-20240229": "July 21st, 2025", "claude-3-opus-20240229": "January 5th, 2026", "claude-2.1": "July 21st, 2025", "claude-2.0": "July 21st, 2025", "claude-3-7-sonnet-latest": "February 19th, 2026", "claude-3-7-sonnet-20250219": "February 19th, 2026", "claude-3-5-haiku-latest": "February 19th, 2026", "claude-3-5-haiku-20241022": "February 19th, 2026" };
var CF = ["claude-opus-4-6"];
G1.Batches = uX;
var e1 = class extends A$ {
  retrieve($, X = {}, J) {
    let { betas: Q } = X ?? {};
    return this._client.get(F$`/v1/models/${$}`, { ...J, headers: n([{ ...Q?.toString() != null ? { "anthropic-beta": Q?.toString() } : void 0 }, J?.headers]) });
  }
  list($ = {}, X) {
    let { betas: J, ...Q } = $ ?? {};
    return this._client.getAPIList("/v1/models", S6, { query: Q, ...X, headers: n([{ ...J?.toString() != null ? { "anthropic-beta": J?.toString() } : void 0 }, X?.headers]) });
  }
};
var mX = ($) => {
  if (typeof globalThis.process < "u") return globalThis.process.env?.[$]?.trim() ?? void 0;
  if (typeof globalThis.Deno < "u") return globalThis.Deno.env?.get?.($)?.trim();
  return;
};
var vz;
var Cz;
var bJ;
var UK;
var HK = "\\n\\nHuman:";
var KK = "\\n\\nAssistant:";
var P$ = class {
  constructor({ baseURL: $ = mX("ANTHROPIC_BASE_URL"), apiKey: X = mX("ANTHROPIC_API_KEY") ?? null, authToken: J = mX("ANTHROPIC_AUTH_TOKEN") ?? null, ...Q } = {}) {
    vz.add(this), bJ.set(this, void 0);
    let Y = { apiKey: X, authToken: J, ...Q, baseURL: $ || "https://api.anthropic.com" };
    if (!Y.dangerouslyAllowBrowser && PH()) throw new T(`It looks like you're running in a browser-like environment.

This is disabled by default, as it risks exposing your secret API credentials to attackers.
If you understand the risks and have appropriate mitigations in place,
you can set the \`dangerouslyAllowBrowser\` option to \`true\`, e.g.,

new Anthropic({ apiKey, dangerouslyAllowBrowser: true });
`);
    this.baseURL = Y.baseURL, this.timeout = Y.timeout ?? Cz.DEFAULT_TIMEOUT, this.logger = Y.logger ?? console;
    let z = "warn";
    this.logLevel = z, this.logLevel = Uz(Y.logLevel, "ClientOptions.logLevel", this) ?? Uz(mX("ANTHROPIC_LOG"), "process.env['ANTHROPIC_LOG']", this) ?? z, this.fetchOptions = Y.fetchOptions, this.maxRetries = Y.maxRetries ?? 2, this.fetch = Y.fetch ?? EH(), v(this, bJ, SH, "f"), this._options = Y, this.apiKey = typeof X === "string" ? X : null, this.authToken = J;
  }
  withOptions($) {
    return new this.constructor({ ...this._options, baseURL: this.baseURL, maxRetries: this.maxRetries, timeout: this.timeout, logger: this.logger, logLevel: this.logLevel, fetch: this.fetch, fetchOptions: this.fetchOptions, apiKey: this.apiKey, authToken: this.authToken, ...$ });
  }
  defaultQuery() {
    return this._options.defaultQuery;
  }
  validateHeaders({ values: $, nulls: X }) {
    if ($.get("x-api-key") || $.get("authorization")) return;
    if (this.apiKey && $.get("x-api-key")) return;
    if (X.has("x-api-key")) return;
    if (this.authToken && $.get("authorization")) return;
    if (X.has("authorization")) return;
    throw Error('Could not resolve authentication method. Expected either apiKey or authToken to be set. Or for one of the "X-Api-Key" or "Authorization" headers to be explicitly omitted');
  }
  async authHeaders($) {
    return n([await this.apiKeyAuth($), await this.bearerAuth($)]);
  }
  async apiKeyAuth($) {
    if (this.apiKey == null) return;
    return n([{ "X-Api-Key": this.apiKey }]);
  }
  async bearerAuth($) {
    if (this.authToken == null) return;
    return n([{ Authorization: `Bearer ${this.authToken}` }]);
  }
  stringifyQuery($) {
    return vH($);
  }
  getUserAgent() {
    return `${this.constructor.name}/JS ${I4}`;
  }
  defaultIdempotencyKey() {
    return `stainless-node-retry-${Jz()}`;
  }
  makeStatusError($, X, J, Q) {
    return v$.generate($, X, J, Q);
  }
  buildURL($, X, J) {
    let Q = !D(this, vz, "m", UK).call(this) && J || this.baseURL, Y = LH($) ? new URL($) : new URL(Q + (Q.endsWith("/") && $.startsWith("/") ? $.slice(1) : $)), z = this.defaultQuery(), W = Object.fromEntries(Y.searchParams);
    if (!zz(z) || !zz(W)) X = { ...W, ...z, ...X };
    if (typeof X === "object" && X && !Array.isArray(X)) Y.search = this.stringifyQuery(X);
    return Y.toString();
  }
  _calculateNonstreamingTimeout($) {
    if (3600 * $ / 128e3 > 600) throw new T("Streaming is required for operations that may take longer than 10 minutes. See https://github.com/anthropics/anthropic-sdk-typescript#streaming-responses for more details");
    return 6e5;
  }
  async prepareOptions($) {
  }
  async prepareRequest($, { url: X, options: J }) {
  }
  get($, X) {
    return this.methodRequest("get", $, X);
  }
  post($, X) {
    return this.methodRequest("post", $, X);
  }
  patch($, X) {
    return this.methodRequest("patch", $, X);
  }
  put($, X) {
    return this.methodRequest("put", $, X);
  }
  delete($, X) {
    return this.methodRequest("delete", $, X);
  }
  methodRequest($, X, J) {
    return this.request(Promise.resolve(J).then((Q) => {
      return { method: $, path: X, ...Q };
    }));
  }
  request($, X = null) {
    return new J1(this, this.makeRequest($, X, void 0));
  }
  async makeRequest($, X, J) {
    let Q = await $, Y = Q.maxRetries ?? this.maxRetries;
    if (X == null) X = Y;
    await this.prepareOptions(Q);
    let { req: z, url: W, timeout: G } = await this.buildRequest(Q, { retryCount: Y - X });
    await this.prepareRequest(z, { url: W, options: Q });
    let U = "log_" + (Math.random() * 16777216 | 0).toString(16).padStart(6, "0"), H = J === void 0 ? "" : `, retryOf: ${J}`, K = Date.now();
    if (_$(this).debug(`[${U}] sending request`, e6({ retryOfRequestLogID: J, method: Q.method, url: W, options: Q, headers: z.headers })), Q.signal?.aborted) throw new T$();
    let V = new AbortController(), O = await this.fetchWithTimeout(W, z, G, V).catch($X), N = Date.now();
    if (O instanceof globalThis.Error) {
      let L = `retrying, ${X} attempts remaining`;
      if (Q.signal?.aborted) throw new T$();
      let j = s6(O) || /timed? ?out/i.test(String(O) + ("cause" in O ? String(O.cause) : ""));
      if (X) return _$(this).info(`[${U}] connection ${j ? "timed out" : "failed"} - ${L}`), _$(this).debug(`[${U}] connection ${j ? "timed out" : "failed"} (${L})`, e6({ retryOfRequestLogID: J, url: W, durationMs: N - K, message: O.message })), this.retryRequest(Q, X, J ?? U);
      if (_$(this).info(`[${U}] connection ${j ? "timed out" : "failed"} - error; no more retries left`), _$(this).debug(`[${U}] connection ${j ? "timed out" : "failed"} (error; no more retries left)`, e6({ retryOfRequestLogID: J, url: W, durationMs: N - K, message: O.message })), j) throw new XX();
      throw new X1({ cause: O });
    }
    let w = [...O.headers.entries()].filter(([L]) => L === "request-id").map(([L, j]) => ", " + L + ": " + JSON.stringify(j)).join(""), B = `[${U}${H}${w}] ${z.method} ${W} ${O.ok ? "succeeded" : "failed"} with status ${O.status} in ${N - K}ms`;
    if (!O.ok) {
      let L = await this.shouldRetry(O);
      if (X && L) {
        let B$ = `retrying, ${X} attempts remaining`;
        return await RH(O.body), _$(this).info(`${B} - ${B$}`), _$(this).debug(`[${U}] response error (${B$})`, e6({ retryOfRequestLogID: J, url: O.url, status: O.status, headers: O.headers, durationMs: N - K })), this.retryRequest(Q, X, J ?? U, O.headers);
      }
      let j = L ? "error; no more retries left" : "error; not retryable";
      _$(this).info(`${B} - ${j}`);
      let I = await O.text().catch((B$) => $X(B$).message), b = e9(I), x = b ? void 0 : I;
      throw _$(this).debug(`[${U}] response error (${j})`, e6({ retryOfRequestLogID: J, url: O.url, status: O.status, headers: O.headers, message: x, durationMs: Date.now() - K })), this.makeStatusError(O.status, b, x, O.headers);
    }
    return _$(this).info(B), _$(this).debug(`[${U}] response start`, e6({ retryOfRequestLogID: J, url: O.url, status: O.status, headers: O.headers, durationMs: N - K })), { response: O, options: Q, controller: V, requestLogID: U, retryOfRequestLogID: J, startTime: K };
  }
  getAPIList($, X, J) {
    return this.requestAPIList(X, J && "then" in J ? J.then((Q) => ({ method: "get", path: $, ...Q })) : { method: "get", path: $, ...J });
  }
  requestAPIList($, X) {
    let J = this.makeRequest(X, null, void 0);
    return new zJ(this, J, $);
  }
  async fetchWithTimeout($, X, J, Q) {
    let { signal: Y, method: z, ...W } = X || {}, G = this._makeAbort(Q);
    if (Y) Y.addEventListener("abort", G, { once: true });
    let U = setTimeout(G, J), H = globalThis.ReadableStream && W.body instanceof globalThis.ReadableStream || typeof W.body === "object" && W.body !== null && Symbol.asyncIterator in W.body, K = { signal: Q.signal, ...H ? { duplex: "half" } : {}, method: "GET", ...W };
    if (z) K.method = z.toUpperCase();
    try {
      return await this.fetch.call(void 0, $, K);
    } finally {
      clearTimeout(U);
    }
  }
  async shouldRetry($) {
    let X = $.headers.get("x-should-retry");
    if (X === "true") return true;
    if (X === "false") return false;
    if ($.status === 408) return true;
    if ($.status === 409) return true;
    if ($.status === 429) return true;
    if ($.status >= 500) return true;
    return false;
  }
  async retryRequest($, X, J, Q) {
    let Y, z = Q?.get("retry-after-ms");
    if (z) {
      let G = parseFloat(z);
      if (!Number.isNaN(G)) Y = G;
    }
    let W = Q?.get("retry-after");
    if (W && !Y) {
      let G = parseFloat(W);
      if (!Number.isNaN(G)) Y = G * 1e3;
      else Y = Date.parse(W) - Date.now();
    }
    if (Y === void 0) {
      let G = $.maxRetries ?? this.maxRetries;
      Y = this.calculateDefaultRetryTimeoutMillis(X, G);
    }
    return await MH(Y), this.makeRequest($, X - 1, J);
  }
  calculateDefaultRetryTimeoutMillis($, X) {
    let Y = X - $, z = Math.min(0.5 * Math.pow(2, Y), 8), W = 1 - Math.random() * 0.25;
    return z * W * 1e3;
  }
  calculateNonstreamingTimeout($, X) {
    if (36e5 * $ / 128e3 > 6e5 || X != null && $ > X) throw new T("Streaming is required for operations that may take longer than 10 minutes. See https://github.com/anthropics/anthropic-sdk-typescript#long-requests for more details");
    return 6e5;
  }
  async buildRequest($, { retryCount: X = 0 } = {}) {
    let J = { ...$ }, { method: Q, path: Y, query: z, defaultBaseURL: W } = J, G = this.buildURL(Y, z, W);
    if ("timeout" in J) FH("timeout", J.timeout);
    J.timeout = J.timeout ?? this.timeout;
    let { bodyHeaders: U, body: H } = this.buildBody({ options: J }), K = await this.buildHeaders({ options: $, method: Q, bodyHeaders: U, retryCount: X });
    return { req: { method: Q, headers: K, ...J.signal && { signal: J.signal }, ...globalThis.ReadableStream && H instanceof globalThis.ReadableStream && { duplex: "half" }, ...H && { body: H }, ...this.fetchOptions ?? {}, ...J.fetchOptions ?? {} }, url: G, timeout: J.timeout };
  }
  async buildHeaders({ options: $, method: X, bodyHeaders: J, retryCount: Q }) {
    let Y = {};
    if (this.idempotencyHeader && X !== "get") {
      if (!$.idempotencyKey) $.idempotencyKey = this.defaultIdempotencyKey();
      Y[this.idempotencyHeader] = $.idempotencyKey;
    }
    let z = n([Y, { Accept: "application/json", "User-Agent": this.getUserAgent(), "X-Stainless-Retry-Count": String(Q), ...$.timeout ? { "X-Stainless-Timeout": String(Math.trunc($.timeout / 1e3)) } : {}, ...ZH(), ...this._options.dangerouslyAllowBrowser ? { "anthropic-dangerous-direct-browser-access": "true" } : void 0, "anthropic-version": "2023-06-01" }, await this.authHeaders($), this._options.defaultHeaders, J, $.headers]);
    return this.validateHeaders(z), z.values;
  }
  _makeAbort($) {
    return () => $.abort();
  }
  buildBody({ options: { body: $, headers: X } }) {
    if (!$) return { bodyHeaders: void 0, body: void 0 };
    let J = n([X]);
    if (ArrayBuffer.isView($) || $ instanceof ArrayBuffer || $ instanceof DataView || typeof $ === "string" && J.values.has("content-type") || globalThis.Blob && $ instanceof globalThis.Blob || $ instanceof FormData || $ instanceof URLSearchParams || globalThis.ReadableStream && $ instanceof globalThis.ReadableStream) return { bodyHeaders: void 0, body: $ };
    else if (typeof $ === "object" && (Symbol.asyncIterator in $ || Symbol.iterator in $ && "next" in $ && typeof $.next === "function")) return { bodyHeaders: void 0, body: $J($) };
    else if (typeof $ === "object" && J.values.get("content-type") === "application/x-www-form-urlencoded") return { bodyHeaders: { "content-type": "application/x-www-form-urlencoded" }, body: this.stringifyQuery($) };
    else return D(this, bJ, "f").call(this, { body: $, headers: J });
  }
};
Cz = P$, bJ = /* @__PURE__ */ new WeakMap(), vz = /* @__PURE__ */ new WeakSet(), UK = function() {
  return this.baseURL !== "https://api.anthropic.com";
};
P$.Anthropic = Cz;
P$.HUMAN_PROMPT = HK;
P$.AI_PROMPT = KK;
P$.DEFAULT_TIMEOUT = 6e5;
P$.AnthropicError = T;
P$.APIError = v$;
P$.APIConnectionError = X1;
P$.APIConnectionTimeoutError = XX;
P$.APIUserAbortError = T$;
P$.NotFoundError = zX;
P$.ConflictError = WX;
P$.RateLimitError = UX;
P$.BadRequestError = JX;
P$.AuthenticationError = YX;
P$.InternalServerError = HX;
P$.PermissionDeniedError = QX;
P$.UnprocessableEntityError = GX;
P$.toFile = WJ;
var U1 = class extends P$ {
  constructor() {
    super(...arguments);
    this.completions = new a1(this), this.messages = new G1(this), this.models = new e1(this), this.beta = new m6(this);
  }
};
U1.Completions = a1;
U1.Messages = G1;
U1.Models = e1;
U1.Beta = m6;
function lX($) {
  return $ instanceof Error ? $.message : String($);
}
function H1($) {
  if ($ && typeof $ === "object" && "code" in $ && typeof $.code === "string") return $.code;
  return;
}
function VK($) {
  return H1($) === "ENOENT";
}
var X0;
var $0 = null;
function yF() {
  if ($0) return $0;
  if (!B6(process.env.DEBUG_CLAUDE_AGENT_SDK)) return X0 = null, $0 = Promise.resolve(), $0;
  let $ = (0, import_path3.join)(c1(), "debug");
  return X0 = (0, import_path3.join)($, `sdk-${(0, import_crypto.randomUUID)()}.txt`), process.stderr.write(`SDK debug logs: ${X0}
`), $0 = (0, import_promises.mkdir)($, { recursive: true }).then(() => {
  }).catch(() => {
  }), $0;
}
function s$($) {
  if (X0 === null) return;
  let J = `${(/* @__PURE__ */ new Date()).toISOString()} ${$}
`;
  yF().then(() => {
    if (X0) (0, import_promises.appendFile)(X0, J).catch(() => {
    });
  });
}
function kz() {
  let $ = /* @__PURE__ */ new Set();
  return { subscribe(X) {
    return $.add(X), () => {
      $.delete(X);
    };
  }, emit(...X) {
    for (let J of $) J(...X);
  }, clear() {
    $.clear();
  } };
}
function gF() {
  let $ = "";
  if (typeof process < "u" && typeof process.cwd === "function" && typeof import_fs.realpathSync === "function") {
    let J = (0, import_process.cwd)();
    try {
      $ = (0, import_fs.realpathSync)(J).normalize("NFC");
    } catch {
      $ = J.normalize("NFC");
    }
  }
  return { originalCwd: $, projectRoot: $, totalCostUSD: 0, totalAPIDuration: 0, totalAPIDurationWithoutRetries: 0, totalToolDuration: 0, turnHookDurationMs: 0, turnToolDurationMs: 0, turnClassifierDurationMs: 0, turnToolCount: 0, turnHookCount: 0, turnClassifierCount: 0, startTime: Date.now(), lastInteractionTime: Date.now(), totalLinesAdded: 0, totalLinesRemoved: 0, hasUnknownModelCost: false, cwd: $, modelUsage: {}, mainLoopModelOverride: void 0, initialMainLoopModel: null, modelStrings: null, isInteractive: false, kairosActive: false, strictToolResultPairing: false, memoryToggledOff: false, sdkAgentProgressSummariesEnabled: false, userMsgOptIn: false, clientType: "cli", sessionSource: void 0, questionPreviewFormat: void 0, sessionIngressToken: void 0, oauthTokenFromFd: void 0, apiKeyFromFd: void 0, flagSettingsPath: void 0, flagSettingsInline: null, allowedSettingSources: ["userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"], meter: null, sessionCounter: null, locCounter: null, prCounter: null, commitCounter: null, costCounter: null, tokenCounter: null, codeEditToolDecisionCounter: null, activeTimeCounter: null, statsStore: null, sessionId: (0, import_crypto2.randomUUID)(), parentSessionId: void 0, loggerProvider: null, eventLogger: null, meterProvider: null, tracerProvider: null, agentColorMap: /* @__PURE__ */ new Map(), agentColorIndex: 0, lastAPIRequest: null, lastAPIRequestMessages: null, lastClassifierRequests: null, cachedClaudeMdContent: null, inMemoryErrorLog: [], inlinePlugins: [], chromeFlagOverride: void 0, useCoworkPlugins: false, sessionBypassPermissionsMode: false, scheduledTasksEnabled: false, sessionCronTasks: [], sessionCreatedTeams: /* @__PURE__ */ new Set(), sessionTrustAccepted: false, sessionPersistenceDisabled: false, hasExitedPlanMode: false, needsPlanModeExitAttachment: false, needsAutoModeExitAttachment: false, lspRecommendationShownThisSession: false, initJsonSchema: null, registeredHooks: null, planSlugCache: /* @__PURE__ */ new Map(), teleportedSessionInfo: null, invokedSkills: /* @__PURE__ */ new Map(), slowOperations: [], sdkBetas: void 0, mainThreadAgentType: void 0, isRemoteMode: false, ...false, directConnectServerUrl: void 0, systemPromptSectionCache: /* @__PURE__ */ new Map(), lastEmittedDate: null, additionalDirectoriesForClaudeMd: [], allowedChannels: [], hasDevChannels: false, sessionProjectDir: null, promptCache1hAllowlist: null, afkModeHeaderLatched: null, fastModeHeaderLatched: null, cacheEditingHeaderLatched: null, thinkingClearLatched: null, promptId: null, lastMainRequestId: void 0, lastApiCompletionTimestamp: null, pendingPostCompaction: false };
}
var hF = gF();
function BK() {
  return hF.sessionId;
}
var uF = kz();
var Su = uF.subscribe;
var mF = kz();
var vu = mF.subscribe;
function qK({ writeFn: $, flushIntervalMs: X = 1e3, maxBufferSize: J = 100, maxBufferBytes: Q = 1 / 0, immediateMode: Y = false }) {
  let z = [], W = 0, G = null, U = null;
  function H() {
    if (G) clearTimeout(G), G = null;
  }
  function K() {
    if (U) $(U.join("")), U = null;
    if (z.length === 0) return;
    $(z.join("")), z = [], W = 0, H();
  }
  function V() {
    if (!G) G = setTimeout(K, X);
  }
  function O() {
    if (U) {
      U.push(...z), z = [], W = 0, H();
      return;
    }
    let N = z;
    z = [], W = 0, H(), U = N, setImmediate(() => {
      let w = U;
      if (U = null, w) $(w.join(""));
    });
  }
  return { write(N) {
    if (Y) {
      $(N);
      return;
    }
    if (z.push(N), W += N.length, V(), z.length >= J || W >= Q) O();
  }, flush: K, dispose() {
    K();
  } };
}
var DK = /* @__PURE__ */ new Set();
function LK($) {
  return DK.add($), () => DK.delete($);
}
var jK = R6(($) => {
  if (!$ || $.trim() === "") return null;
  let X = $.split(",").map((z) => z.trim()).filter(Boolean);
  if (X.length === 0) return null;
  let J = X.some((z) => z.startsWith("!")), Q = X.some((z) => !z.startsWith("!"));
  if (J && Q) return null;
  let Y = X.map((z) => z.replace(/^!/, "").toLowerCase());
  return { include: J ? [] : Y, exclude: J ? Y : [], isExclusive: J };
});
function lF($) {
  let X = [], J = $.match(/^MCP server ["']([^"']+)["']/);
  if (J && J[1]) X.push("mcp"), X.push(J[1].toLowerCase());
  else {
    let z = $.match(/^([^:[]+):/);
    if (z && z[1]) X.push(z[1].trim().toLowerCase());
  }
  let Q = $.match(/^\[([^\]]+)]/);
  if (Q && Q[1]) X.push(Q[1].trim().toLowerCase());
  if ($.toLowerCase().includes("1p event:")) X.push("1p");
  let Y = $.match(/:\s*([^:]+?)(?:\s+(?:type|mode|status|event))?:/);
  if (Y && Y[1]) {
    let z = Y[1].trim().toLowerCase();
    if (z.length < 30 && !z.includes(" ")) X.push(z);
  }
  return Array.from(new Set(X));
}
function cF($, X) {
  if (!X) return true;
  if ($.length === 0) return false;
  if (X.isExclusive) return !$.some((J) => X.exclude.includes(J));
  else return $.some((J) => X.include.includes(J));
}
function FK($, X) {
  if (!X) return true;
  let J = lF($);
  return cF(J, X);
}
var sF = { cwd() {
  return process.cwd();
}, existsSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.existsSync(${$})`, 0);
    return r.existsSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, async stat($) {
  return (0, import_promises3.stat)($);
}, async readdir($) {
  return (0, import_promises3.readdir)($, { withFileTypes: true });
}, async unlink($) {
  return (0, import_promises3.unlink)($);
}, async rmdir($) {
  return (0, import_promises3.rmdir)($);
}, async rm($, X) {
  return (0, import_promises3.rm)($, X);
}, async mkdir($, X) {
  try {
    await (0, import_promises3.mkdir)($, { recursive: true, ...X });
  } catch (J) {
    if (H1(J) !== "EEXIST") throw J;
  }
}, async readFile($, X) {
  return (0, import_promises3.readFile)($, { encoding: X.encoding });
}, async rename($, X) {
  return (0, import_promises3.rename)($, X);
}, statSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.statSync(${$})`, 0);
    return r.statSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, lstatSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.lstatSync(${$})`, 0);
    return r.lstatSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, readFileSync($, X) {
  let Q = [];
  try {
    const J = N$(Q, b$`fs.readFileSync(${$})`, 0);
    return r.readFileSync($, { encoding: X.encoding });
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, readFileBytesSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.readFileBytesSync(${$})`, 0);
    return r.readFileSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, readSync($, X) {
  let Y = [];
  try {
    const J = N$(Y, b$`fs.readSync(${$}, ${X.length} bytes)`, 0);
    let Q = void 0;
    try {
      Q = r.openSync($, "r");
      let U = Buffer.alloc(X.length), H = r.readSync(Q, U, 0, X.length, 0);
      return { buffer: U, bytesRead: H };
    } finally {
      if (Q) r.closeSync(Q);
    }
  } catch (z) {
    var W = z, G = 1;
  } finally {
    V$(Y, W, G);
  }
}, appendFileSync($, X, J) {
  let Y = [];
  try {
    const Q = N$(Y, b$`fs.appendFileSync(${$}, ${X.length} chars)`, 0);
    if (J?.mode !== void 0) try {
      let U = r.openSync($, "ax", J.mode);
      try {
        r.appendFileSync(U, X);
      } finally {
        r.closeSync(U);
      }
      return;
    } catch (U) {
      if (H1(U) !== "EEXIST") throw U;
    }
    r.appendFileSync($, X);
  } catch (z) {
    var W = z, G = 1;
  } finally {
    V$(Y, W, G);
  }
}, copyFileSync($, X) {
  let Q = [];
  try {
    const J = N$(Q, b$`fs.copyFileSync(${$} → ${X})`, 0);
    r.copyFileSync($, X);
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, unlinkSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.unlinkSync(${$})`, 0);
    r.unlinkSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, renameSync($, X) {
  let Q = [];
  try {
    const J = N$(Q, b$`fs.renameSync(${$} → ${X})`, 0);
    r.renameSync($, X);
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, linkSync($, X) {
  let Q = [];
  try {
    const J = N$(Q, b$`fs.linkSync(${$} → ${X})`, 0);
    r.linkSync($, X);
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, symlinkSync($, X, J) {
  let Y = [];
  try {
    const Q = N$(Y, b$`fs.symlinkSync(${$} → ${X})`, 0);
    r.symlinkSync($, X, J);
  } catch (z) {
    var W = z, G = 1;
  } finally {
    V$(Y, W, G);
  }
}, readlinkSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.readlinkSync(${$})`, 0);
    return r.readlinkSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, realpathSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.realpathSync(${$})`, 0);
    return r.realpathSync($).normalize("NFC");
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, mkdirSync($, X) {
  let Y = [];
  try {
    const J = N$(Y, b$`fs.mkdirSync(${$})`, 0);
    let Q = { recursive: true };
    if (X?.mode !== void 0) Q.mode = X.mode;
    try {
      r.mkdirSync($, Q);
    } catch (U) {
      if (H1(U) !== "EEXIST") throw U;
    }
  } catch (z) {
    var W = z, G = 1;
  } finally {
    V$(Y, W, G);
  }
}, readdirSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.readdirSync(${$})`, 0);
    return r.readdirSync($, { withFileTypes: true });
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, readdirStringSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.readdirStringSync(${$})`, 0);
    return r.readdirSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, isDirEmptySync($) {
  let Q = [];
  try {
    const X = N$(Q, b$`fs.isDirEmptySync(${$})`, 0);
    let J = this.readdirSync($);
    return J.length === 0;
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, rmdirSync($) {
  let J = [];
  try {
    const X = N$(J, b$`fs.rmdirSync(${$})`, 0);
    r.rmdirSync($);
  } catch (Q) {
    var Y = Q, z = 1;
  } finally {
    V$(J, Y, z);
  }
}, rmSync($, X) {
  let Q = [];
  try {
    const J = N$(Q, b$`fs.rmSync(${$})`, 0);
    r.rmSync($, X);
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
}, createWriteStream($) {
  return r.createWriteStream($);
}, async readFileBytes($, X) {
  if (X === void 0) return (0, import_promises3.readFile)($);
  let J = await (0, import_promises3.open)($, "r");
  try {
    let { size: Q } = await J.stat(), Y = Math.min(Q, X), z = Buffer.allocUnsafe(Y), W = 0;
    while (W < Y) {
      let { bytesRead: G } = await J.read(z, W, Y - W, W);
      if (G === 0) break;
      W += G;
    }
    return W < Y ? z.subarray(0, W) : z;
  } finally {
    await J.close();
  }
} };
var eF = sF;
function _z() {
  return eF;
}
function $M($, X) {
  if ($.destroyed) return;
  $.write(X);
}
function IK($) {
  $M(process.stderr, $);
}
var Tz = { verbose: 0, debug: 1, info: 2, warn: 3, error: 4 };
var zM = R6(() => {
  let $ = process.env.CLAUDE_CODE_DEBUG_LOG_LEVEL?.toLowerCase().trim();
  if ($ && Object.hasOwn(Tz, $)) return $;
  return "debug";
});
var WM = false;
var yz = R6(() => {
  return WM || B6(process.env.DEBUG) || B6(process.env.DEBUG_SDK) || process.argv.includes("--debug") || process.argv.includes("-d") || PK() || process.argv.some(($) => $.startsWith("--debug=")) || ZK() !== null;
});
var GM = R6(() => {
  let $ = process.argv.find((J) => J.startsWith("--debug="));
  if (!$) return null;
  let X = $.substring(8);
  return jK(X);
});
var PK = R6(() => {
  return process.argv.includes("--debug-to-stderr") || process.argv.includes("-d2e");
});
var ZK = R6(() => {
  for (let $ = 0; $ < process.argv.length; $++) {
    let X = process.argv[$];
    if (X.startsWith("--debug-file=")) return X.substring(13);
    if (X === "--debug-file" && $ + 1 < process.argv.length) return process.argv[$ + 1];
  }
  return null;
});
function UM($) {
  if (!yz()) return false;
  if (typeof process > "u" || typeof process.versions > "u" || typeof process.versions.node > "u") return false;
  let X = GM();
  return FK($, X);
}
var HM = false;
var ZJ = null;
var xz = Promise.resolve();
async function KM($, X, J, Q) {
  if ($) await (0, import_promises2.mkdir)(X, { recursive: true }).catch(() => {
  });
  await (0, import_promises2.appendFile)(J, Q), RK();
}
function NM() {
}
function VM() {
  if (!ZJ) {
    let $ = null;
    ZJ = qK({ writeFn: (X) => {
      let J = EK(), Q = (0, import_path4.dirname)(J), Y = $ !== Q;
      if ($ = Q, yz()) {
        if (Y) try {
          _z().mkdirSync(Q);
        } catch {
        }
        _z().appendFileSync(J, X), RK();
        return;
      }
      xz = xz.then(KM.bind(null, Y, Q, J, X)).catch(NM);
    }, flushIntervalMs: 1e3, maxBufferSize: 100, immediateMode: yz() }), LK(async () => {
      ZJ?.dispose(), await xz;
    });
  }
  return ZJ;
}
function L6($, { level: X } = { level: "debug" }) {
  if (Tz[X] < Tz[zM()]) return;
  if (!UM($)) return;
  if (HM && $.includes(`
`)) $ = q$($);
  let Q = `${(/* @__PURE__ */ new Date()).toISOString()} [${X.toUpperCase()}] ${$.trim()}
`;
  if (PK()) {
    IK(Q);
    return;
  }
  VM().write(Q);
}
function EK() {
  return ZK() ?? process.env.CLAUDE_CODE_DEBUG_LOGS_DIR ?? (0, import_path4.join)(c1(), "debug", `${BK()}.txt`);
}
var RK = R6(async () => {
  try {
    let $ = EK(), X = (0, import_path4.dirname)($), J = (0, import_path4.join)(X, "latest");
    await (0, import_promises2.unlink)(J).catch(() => {
    }), await (0, import_promises2.symlink)($, J);
  } catch {
  }
});
var Qm = (() => {
  let $ = process.env.CLAUDE_CODE_SLOW_OPERATION_THRESHOLD_MS;
  if ($ !== void 0) {
    let X = Number($);
    if (!Number.isNaN(X) && X >= 0) return X;
  }
  return 1 / 0;
})();
var OM = { [Symbol.dispose]() {
} };
function wM() {
  return OM;
}
var b$ = wM;
function q$($, X, J) {
  let Y = [];
  try {
    const Q = N$(Y, b$`JSON.stringify(${$})`, 0);
    return JSON.stringify($, X, J);
  } catch (z) {
    var W = z, G = 1;
  } finally {
    V$(Y, W, G);
  }
}
var j6 = ($, X) => {
  let Q = [];
  try {
    const J = N$(Q, b$`JSON.parse(${$})`, 0);
    return typeof X > "u" ? JSON.parse($) : JSON.parse($, X);
  } catch (Y) {
    var z = Y, W = 1;
  } finally {
    V$(Q, z, W);
  }
};
function BM($) {
  let X = $.trim();
  return X.startsWith("{") && X.endsWith("}");
}
function SK($, X) {
  let J = { ...$ };
  if (X) {
    let Q = X.enabled === true && X.failIfUnavailable === void 0 ? { ...X, failIfUnavailable: true } : X, Y = J.settings;
    if (Y && !BM(Y)) throw Error("Cannot use both a settings file path and the sandbox option. Include the sandbox configuration in your settings file instead.");
    let z = { sandbox: Q };
    if (Y) try {
      z = { ...j6(Y), sandbox: Q };
    } catch {
    }
    J.settings = q$(z);
  }
  return J;
}
var LM = 2e3;
var cX = class {
  options;
  process;
  processStdin;
  processStdout;
  ready = false;
  abortController;
  exitError;
  exitListeners = [];
  processExitHandler;
  abortHandler;
  constructor($) {
    this.options = $;
    this.abortController = $.abortController || y1(), this.initialize();
  }
  getDefaultExecutable() {
    return f1() ? "bun" : "node";
  }
  spawnLocalProcess($) {
    let { command: X, args: J, cwd: Q, env: Y, signal: z } = $, W = B6(Y.DEBUG_CLAUDE_AGENT_SDK) || this.options.stderr ? "pipe" : "ignore", G = (0, import_child_process.spawn)(X, J, { cwd: Q, stdio: ["pipe", "pipe", W], signal: z, env: Y, windowsHide: true });
    if (B6(Y.DEBUG_CLAUDE_AGENT_SDK) || this.options.stderr) G.stderr.on("data", (H) => {
      let K = H.toString();
      if (s$(K), this.options.stderr) this.options.stderr(K);
    });
    return { stdin: G.stdin, stdout: G.stdout, get killed() {
      return G.killed;
    }, get exitCode() {
      return G.exitCode;
    }, kill: G.kill.bind(G), on: G.on.bind(G), once: G.once.bind(G), off: G.off.bind(G) };
  }
  initialize() {
    try {
      let { additionalDirectories: $ = [], agent: X, betas: J, cwd: Q, executable: Y = this.getDefaultExecutable(), executableArgs: z = [], extraArgs: W = {}, pathToClaudeCodeExecutable: G, env: U = { ...process.env }, thinkingConfig: H, maxTurns: K, maxBudgetUsd: V, taskBudget: O, model: N, fallbackModel: w, jsonSchema: B, permissionMode: L, allowDangerouslySkipPermissions: j, permissionPromptToolName: I, continueConversation: b, resume: x, settingSources: h, allowedTools: B$ = [], disallowedTools: x$ = [], tools: G6, mcpServers: o6, strictMcpConfig: u6, canUseTool: a4, includePartialMessages: _1, plugins: t6, sandbox: r0 } = this.options, p = ["--output-format", "stream-json", "--verbose", "--input-format", "stream-json"];
      if (H) switch (H.type) {
        case "enabled":
          if (H.budgetTokens === void 0) p.push("--thinking", "adaptive");
          else p.push("--max-thinking-tokens", H.budgetTokens.toString());
          break;
        case "disabled":
          p.push("--thinking", "disabled");
          break;
        case "adaptive":
          p.push("--thinking", "adaptive");
          break;
      }
      if (this.options.effort) p.push("--effort", this.options.effort);
      if (K) p.push("--max-turns", K.toString());
      if (V !== void 0) p.push("--max-budget-usd", V.toString());
      if (O) p.push("--task-budget", O.total.toString());
      if (N) p.push("--model", N);
      if (X) p.push("--agent", X);
      if (J && J.length > 0) p.push("--betas", J.join(","));
      if (B) p.push("--json-schema", q$(B));
      if (this.options.debugFile) p.push("--debug-file", this.options.debugFile);
      else if (this.options.debug) p.push("--debug");
      if (B6(U.DEBUG_CLAUDE_AGENT_SDK)) p.push("--debug-to-stderr");
      if (a4) {
        if (I) throw Error("canUseTool callback cannot be used with permissionPromptToolName. Please use one or the other.");
        p.push("--permission-prompt-tool", "stdio");
      } else if (I) p.push("--permission-prompt-tool", I);
      if (b) p.push("--continue");
      if (x) p.push("--resume", x);
      if (this.options.proactive) p.push("--proactive");
      if (this.options.assistant) p.push("--assistant");
      if (this.options.channels && this.options.channels.length > 0) p.push("--channels", ...this.options.channels);
      if (B$.length > 0) p.push("--allowedTools", B$.join(","));
      if (x$.length > 0) p.push("--disallowedTools", x$.join(","));
      if (G6 !== void 0) if (Array.isArray(G6)) if (G6.length === 0) p.push("--tools", "");
      else p.push("--tools", G6.join(","));
      else p.push("--tools", "default");
      if (o6 && Object.keys(o6).length > 0) p.push("--mcp-config", q$({ mcpServers: o6 }));
      if (h !== void 0) p.push(`--setting-sources=${h.join(",")}`);
      if (u6) p.push("--strict-mcp-config");
      if (L) p.push("--permission-mode", L);
      if (j) p.push("--allow-dangerously-skip-permissions");
      if (w) {
        if (N && w === N) throw Error("Fallback model cannot be the same as the main model. Please specify a different model for fallbackModel option.");
        p.push("--fallback-model", w);
      }
      if (this.options.includeHookEvents) p.push("--include-hook-events");
      if (_1) p.push("--include-partial-messages");
      for (let p$ of $) p.push("--add-dir", p$);
      if (t6 && t6.length > 0) for (let p$ of t6) if (p$.type === "local") p.push("--plugin-dir", p$.path);
      else throw Error(`Unsupported plugin type: ${p$.type}`);
      if (this.options.forkSession) p.push("--fork-session");
      if (this.options.resumeSessionAt) p.push("--resume-session-at", this.options.resumeSessionAt);
      if (this.options.sessionId) p.push("--session-id", this.options.sessionId);
      if (this.options.persistSession === false) p.push("--no-session-persistence");
      let n9 = { ...W ?? {} };
      if (this.options.settings) n9.settings = this.options.settings;
      let aQ = SK(n9, r0);
      for (let [p$, j4] of Object.entries(aQ)) if (j4 === null) p.push(`--${p$}`);
      else p.push(`--${p$}`, j4);
      if (!U.CLAUDE_CODE_ENTRYPOINT) U.CLAUDE_CODE_ENTRYPOINT = "sdk-ts";
      if (delete U.NODE_OPTIONS, B6(U.DEBUG_CLAUDE_AGENT_SDK)) U.DEBUG = "1";
      else delete U.DEBUG;
      let o0 = jM(G), t0 = o0 ? G : Y, s4 = o0 ? [...z, ...p] : [...z, G, ...p], d9 = { command: t0, args: s4, cwd: Q, env: U, signal: this.abortController.signal };
      if (this.options.spawnClaudeCodeProcess) s$(`Spawning Claude Code (custom): ${t0} ${s4.join(" ")}`), this.process = this.options.spawnClaudeCodeProcess(d9);
      else s$(`Spawning Claude Code: ${t0} ${s4.join(" ")}`), this.process = this.spawnLocalProcess(d9);
      this.processStdin = this.process.stdin, this.processStdout = this.process.stdout;
      let x1 = () => {
        if (this.process && !this.process.killed) this.process.kill("SIGTERM");
      };
      this.processExitHandler = x1, this.abortHandler = x1, process.on("exit", this.processExitHandler), this.abortController.signal.addEventListener("abort", this.abortHandler), this.process.on("error", (p$) => {
        if (this.ready = false, this.abortController.signal.aborted) this.exitError = new a$("Claude Code process aborted by user");
        else if (VK(p$)) {
          let j4 = o0 ? `Claude Code native binary not found at ${G}. Please ensure Claude Code is installed via native installer or specify a valid path with options.pathToClaudeCodeExecutable.` : `Claude Code executable not found at ${G}. Is options.pathToClaudeCodeExecutable set?`;
          this.exitError = ReferenceError(j4), s$(this.exitError.message);
        } else this.exitError = Error(`Failed to spawn Claude Code process: ${p$.message}`), s$(this.exitError.message);
      }), this.process.on("exit", (p$, j4) => {
        if (this.ready = false, this.abortController.signal.aborted) this.exitError = new a$("Claude Code process aborted by user");
        else {
          let a0 = this.getProcessExitError(p$, j4);
          if (a0) this.exitError = a0, s$(a0.message);
        }
      }), this.ready = true;
    } catch ($) {
      throw this.ready = false, $;
    }
  }
  getProcessExitError($, X) {
    if ($ !== 0 && $ !== null) return Error(`Claude Code process exited with code ${$}`);
    else if (X) return Error(`Claude Code process terminated by signal ${X}`);
    return;
  }
  write($) {
    if (this.abortController.signal.aborted) throw new a$("Operation aborted");
    if (!this.ready || !this.processStdin) throw Error("ProcessTransport is not ready for writing");
    if (this.processStdin.writableEnded) {
      s$("[ProcessTransport] Dropping write to ended stdin stream");
      return;
    }
    if (this.process?.killed || this.process?.exitCode !== null) throw Error("Cannot write to terminated process");
    if (this.exitError) throw Error(`Cannot write to process that exited with error: ${this.exitError.message}`);
    s$(`[ProcessTransport] Writing to stdin: ${$.substring(0, 100)}`);
    try {
      if (!this.processStdin.write($)) s$("[ProcessTransport] Write buffer full, data queued");
    } catch (X) {
      throw this.ready = false, Error(`Failed to write to process stdin: ${lX(X)}`);
    }
  }
  close() {
    if (this.processStdin) this.processStdin.end(), this.processStdin = void 0;
    if (this.abortHandler) this.abortController.signal.removeEventListener("abort", this.abortHandler), this.abortHandler = void 0;
    for (let { handler: X } of this.exitListeners) this.process?.off("exit", X);
    this.exitListeners = [];
    let $ = this.process;
    if ($ && !$.killed && $.exitCode === null) setTimeout((X) => {
      if (X.killed || X.exitCode !== null) return;
      X.kill("SIGTERM"), setTimeout((J) => {
        if (J.exitCode === null) J.kill("SIGKILL");
      }, 5e3, X).unref();
    }, LM, $).unref(), $.once("exit", () => {
      if (this.processExitHandler) process.off("exit", this.processExitHandler), this.processExitHandler = void 0;
    });
    else if (this.processExitHandler) process.off("exit", this.processExitHandler), this.processExitHandler = void 0;
    this.ready = false;
  }
  isReady() {
    return this.ready;
  }
  async *readMessages() {
    if (!this.processStdout) throw Error("ProcessTransport output stream not available");
    if (this.exitError) throw this.exitError;
    let $ = (0, import_readline.createInterface)({ input: this.processStdout }), X = this.process ? (() => {
      let J = this.process, Q = () => $.close();
      return J.on("error", Q), () => J.off("error", Q);
    })() : void 0;
    if (this.exitError) $.close();
    try {
      for await (let J of $) if (J.trim()) {
        let Q;
        try {
          Q = j6(J);
        } catch (Y) {
          s$(`Non-JSON stdout: ${J}`);
          continue;
        }
        yield Q;
      }
      if (this.exitError) throw this.exitError;
      await this.waitForExit();
    } catch (J) {
      throw J;
    } finally {
      X?.(), $.close();
    }
  }
  endInput() {
    if (this.processStdin) this.processStdin.end();
  }
  getInputStream() {
    return this.processStdin;
  }
  onExit($) {
    if (!this.process) return () => {
    };
    let X = (J, Q) => {
      let Y = this.getProcessExitError(J, Q);
      $(Y);
    };
    return this.process.on("exit", X), this.exitListeners.push({ callback: $, handler: X }), () => {
      if (this.process) this.process.off("exit", X);
      let J = this.exitListeners.findIndex((Q) => Q.handler === X);
      if (J !== -1) this.exitListeners.splice(J, 1);
    };
  }
  async waitForExit() {
    if (!this.process) {
      if (this.exitError) throw this.exitError;
      return;
    }
    if (this.process.exitCode !== null || this.process.killed || this.exitError) {
      if (this.exitError) throw this.exitError;
      return;
    }
    return new Promise(($, X) => {
      let J = (Y, z) => {
        if (this.abortController.signal.aborted) {
          X(new a$("Operation aborted"));
          return;
        }
        let W = this.getProcessExitError(Y, z);
        if (W) X(W);
        else $();
      };
      this.process.once("exit", J);
      let Q = (Y) => {
        this.process.off("exit", J), X(Y);
      };
      this.process.once("error", Q), this.process.once("exit", () => {
        this.process.off("error", Q);
      });
    });
  }
};
function jM($) {
  return ![".js", ".mjs", ".tsx", ".ts", ".jsx"].some((J) => $.endsWith(J));
}
var K1 = class {
  returned;
  queue = [];
  readResolve;
  readReject;
  isDone = false;
  hasError;
  started = false;
  constructor($) {
    this.returned = $;
  }
  [Symbol.asyncIterator]() {
    if (this.started) throw Error("Stream can only be iterated once");
    return this.started = true, this;
  }
  next() {
    if (this.queue.length > 0) return Promise.resolve({ done: false, value: this.queue.shift() });
    if (this.isDone) return Promise.resolve({ done: true, value: void 0 });
    if (this.hasError) return Promise.reject(this.hasError);
    return new Promise(($, X) => {
      this.readResolve = $, this.readReject = X;
    });
  }
  enqueue($) {
    if (this.readResolve) {
      let X = this.readResolve;
      this.readResolve = void 0, this.readReject = void 0, X({ done: false, value: $ });
    } else this.queue.push($);
  }
  done() {
    if (this.isDone = true, this.readResolve) {
      let $ = this.readResolve;
      this.readResolve = void 0, this.readReject = void 0, $({ done: true, value: void 0 });
    }
  }
  error($) {
    if (this.hasError = $, this.readReject) {
      let X = this.readReject;
      this.readResolve = void 0, this.readReject = void 0, X($);
    }
  }
  return() {
    if (this.isDone = true, this.returned) this.returned();
    return Promise.resolve({ done: true, value: void 0 });
  }
};
var fz = class {
  sendMcpMessage;
  isClosed = false;
  constructor($) {
    this.sendMcpMessage = $;
  }
  onclose;
  onerror;
  onmessage;
  async start() {
  }
  async send($) {
    if (this.isClosed) throw Error("Transport is closed");
    this.sendMcpMessage($);
  }
  async close() {
    if (this.isClosed) return;
    this.isClosed = true, this.onclose?.();
  }
};
var pX = class {
  transport;
  isSingleUserTurn;
  canUseTool;
  hooks;
  abortController;
  jsonSchema;
  initConfig;
  onElicitation;
  pendingControlResponses = /* @__PURE__ */ new Map();
  cleanupPerformed = false;
  sdkMessages;
  inputStream = new K1();
  initialization;
  cancelControllers = /* @__PURE__ */ new Map();
  hookCallbacks = /* @__PURE__ */ new Map();
  nextCallbackId = 0;
  sdkMcpTransports = /* @__PURE__ */ new Map();
  sdkMcpServerInstances = /* @__PURE__ */ new Map();
  pendingMcpResponses = /* @__PURE__ */ new Map();
  firstResultReceivedResolve;
  firstResultReceived = false;
  lastErrorResultText;
  setIsSingleUserTurn($) {
    this.isSingleUserTurn = $;
  }
  hasBidirectionalNeeds() {
    return this.sdkMcpTransports.size > 0 || this.hooks !== void 0 && Object.keys(this.hooks).length > 0 || this.canUseTool !== void 0 || this.onElicitation !== void 0;
  }
  constructor($, X, J, Q, Y, z = /* @__PURE__ */ new Map(), W, G, U) {
    this.transport = $;
    this.isSingleUserTurn = X;
    this.canUseTool = J;
    this.hooks = Q;
    this.abortController = Y;
    this.jsonSchema = W;
    this.initConfig = G;
    this.onElicitation = U;
    for (let [H, K] of z) this.connectSdkMcpServer(H, K);
    this.sdkMessages = this.readSdkMessages(), this.readMessages(), this.initialization = this.initialize(), this.initialization.catch(() => {
    });
  }
  setError($) {
    this.inputStream.error($);
  }
  async stopTask($) {
    await this.request({ subtype: "stop_task", task_id: $ });
  }
  close() {
    this.cleanup();
  }
  cleanup($) {
    if (this.cleanupPerformed) return;
    this.cleanupPerformed = true;
    try {
      for (let J of this.cancelControllers.values()) J.abort();
      this.cancelControllers.clear(), this.transport.close();
      let X = Error("Query closed before response received");
      for (let { reject: J } of this.pendingControlResponses.values()) J(X);
      this.pendingControlResponses.clear();
      for (let { reject: J } of this.pendingMcpResponses.values()) J(X);
      this.pendingMcpResponses.clear(), this.hookCallbacks.clear();
      for (let J of this.sdkMcpTransports.values()) J.close().catch(() => {
      });
      if (this.sdkMcpTransports.clear(), $) this.inputStream.error($);
      else this.inputStream.done();
    } catch (X) {
    }
  }
  next(...[$]) {
    return this.sdkMessages.next(...[$]);
  }
  return($) {
    return this.sdkMessages.return($);
  }
  throw($) {
    return this.sdkMessages.throw($);
  }
  [Symbol.asyncIterator]() {
    return this.sdkMessages;
  }
  [Symbol.asyncDispose]() {
    return this.sdkMessages[Symbol.asyncDispose]();
  }
  async readMessages() {
    try {
      for await (let $ of this.transport.readMessages()) {
        if ($.type === "control_response") {
          let X = this.pendingControlResponses.get($.response.request_id);
          if (X) X.handler($.response);
          continue;
        } else if ($.type === "control_request") {
          this.handleControlRequest($);
          continue;
        } else if ($.type === "control_cancel_request") {
          this.handleControlCancelRequest($);
          continue;
        } else if ($.type === "keep_alive") continue;
        if ($.type === "system" && $.subtype === "post_turn_summary") continue;
        if ($.type === "result") {
          if (this.lastErrorResultText = $.is_error ? $.subtype === "success" ? $.result : $.errors.join("; ") : void 0, this.firstResultReceived = true, this.firstResultReceivedResolve) this.firstResultReceivedResolve();
          if (this.isSingleUserTurn) L6("[Query.readMessages] First result received for single-turn query, closing stdin"), this.transport.endInput();
        } else if (!($.type === "system" && $.subtype === "session_state_changed")) this.lastErrorResultText = void 0;
        this.inputStream.enqueue($);
      }
      if (this.firstResultReceivedResolve) this.firstResultReceivedResolve();
      this.inputStream.done(), this.cleanup();
    } catch ($) {
      if (this.firstResultReceivedResolve) this.firstResultReceivedResolve();
      if (this.lastErrorResultText !== void 0 && !($ instanceof a$)) {
        let X = Error(`Claude Code returned an error result: ${this.lastErrorResultText}`);
        L6(`[Query.readMessages] Replacing exit error with result text. Original: ${lX($)}`), this.inputStream.error(X), this.cleanup(X);
        return;
      }
      this.inputStream.error($), this.cleanup($);
    }
  }
  async handleControlRequest($) {
    let X = new AbortController();
    this.cancelControllers.set($.request_id, X);
    try {
      let J = await this.processControlRequest($, X.signal);
      if (this.cleanupPerformed) return;
      let Q = { type: "control_response", response: { subtype: "success", request_id: $.request_id, response: J } };
      await Promise.resolve(this.transport.write(q$(Q) + `
`));
    } catch (J) {
      if (this.cleanupPerformed) return;
      let Q = { type: "control_response", response: { subtype: "error", request_id: $.request_id, error: lX(J) } };
      await Promise.resolve(this.transport.write(q$(Q) + `
`));
    } finally {
      this.cancelControllers.delete($.request_id);
    }
  }
  handleControlCancelRequest($) {
    let X = this.cancelControllers.get($.request_id);
    if (X) X.abort(), this.cancelControllers.delete($.request_id);
  }
  async processControlRequest($, X) {
    if ($.request.subtype === "can_use_tool") {
      if (!this.canUseTool) throw Error("canUseTool callback is not provided.");
      return { ...await this.canUseTool($.request.tool_name, $.request.input, { signal: X, suggestions: $.request.permission_suggestions, blockedPath: $.request.blocked_path, decisionReason: $.request.decision_reason, title: $.request.title, displayName: $.request.display_name, description: $.request.description, toolUseID: $.request.tool_use_id, agentID: $.request.agent_id }), toolUseID: $.request.tool_use_id };
    } else if ($.request.subtype === "hook_callback") return await this.handleHookCallbacks($.request.callback_id, $.request.input, $.request.tool_use_id, X);
    else if ($.request.subtype === "mcp_message") {
      let J = $.request, Q = this.sdkMcpTransports.get(J.server_name);
      if (!Q) throw Error(`SDK MCP server not found: ${J.server_name}`);
      if ("method" in J.message && "id" in J.message && J.message.id !== null) return { mcp_response: await this.handleMcpControlRequest(J.server_name, J, Q) };
      else {
        if (Q.onmessage) Q.onmessage(J.message);
        return { mcp_response: { jsonrpc: "2.0", result: {}, id: 0 } };
      }
    } else if ($.request.subtype === "elicitation") {
      let J = $.request;
      if (this.onElicitation) return await this.onElicitation({ serverName: J.mcp_server_name, message: J.message, mode: J.mode, url: J.url, elicitationId: J.elicitation_id, requestedSchema: J.requested_schema }, { signal: X });
      return { action: "decline" };
    }
    throw Error("Unsupported control request subtype: " + $.request.subtype);
  }
  async *readSdkMessages() {
    for await (let $ of this.inputStream) yield $;
  }
  async initialize() {
    let $;
    if (this.hooks) {
      $ = {};
      for (let [Y, z] of Object.entries(this.hooks)) if (z.length > 0) $[Y] = z.map((W) => {
        let G = [];
        for (let U of W.hooks) {
          let H = `hook_${this.nextCallbackId++}`;
          this.hookCallbacks.set(H, U), G.push(H);
        }
        return { matcher: W.matcher, hookCallbackIds: G, timeout: W.timeout };
      });
    }
    let X = this.sdkMcpTransports.size > 0 ? Array.from(this.sdkMcpTransports.keys()) : void 0, J = { subtype: "initialize", hooks: $, sdkMcpServers: X, jsonSchema: this.jsonSchema, systemPrompt: this.initConfig?.systemPrompt, appendSystemPrompt: this.initConfig?.appendSystemPrompt, agents: this.initConfig?.agents, promptSuggestions: this.initConfig?.promptSuggestions, agentProgressSummaries: this.initConfig?.agentProgressSummaries };
    return (await this.request(J)).response;
  }
  async interrupt() {
    await this.request({ subtype: "interrupt" });
  }
  async setPermissionMode($) {
    await this.request({ subtype: "set_permission_mode", mode: $ });
  }
  async setModel($) {
    await this.request({ subtype: "set_model", model: $ });
  }
  async setMaxThinkingTokens($) {
    await this.request({ subtype: "set_max_thinking_tokens", max_thinking_tokens: $ });
  }
  async applyFlagSettings($) {
    await this.request({ subtype: "apply_flag_settings", settings: $ });
  }
  async getSettings() {
    return (await this.request({ subtype: "get_settings" })).response;
  }
  async rewindFiles($, X) {
    return (await this.request({ subtype: "rewind_files", user_message_id: $, dry_run: X?.dryRun })).response;
  }
  async cancelAsyncMessage($) {
    return (await this.request({ subtype: "cancel_async_message", message_uuid: $ })).response.cancelled;
  }
  async seedReadState($, X) {
    await this.request({ subtype: "seed_read_state", path: $, mtime: X });
  }
  async enableRemoteControl($) {
    return (await this.request({ subtype: "remote_control", enabled: $ })).response;
  }
  async setProactive($) {
    await this.request({ subtype: "set_proactive", enabled: $ });
  }
  async generateSessionTitle($, X) {
    return (await this.request({ subtype: "generate_session_title", description: $, persist: X?.persist })).response.title;
  }
  async askSideQuestion($) {
    return (await this.request({ subtype: "side_question", question: $ })).response.response;
  }
  processPendingPermissionRequests($) {
    for (let X of $) if (X.request.subtype === "can_use_tool") this.handleControlRequest(X).catch(() => {
    });
  }
  request($) {
    let X = Math.random().toString(36).substring(2, 15), J = { request_id: X, type: "control_request", request: $ };
    return new Promise((Q, Y) => {
      this.pendingControlResponses.set(X, { handler: (z) => {
        if (this.pendingControlResponses.delete(X), z.subtype === "success") Q(z);
        else if (Y(Error(z.error)), z.pending_permission_requests) this.processPendingPermissionRequests(z.pending_permission_requests);
      }, reject: Y }), Promise.resolve(this.transport.write(q$(J) + `
`)).catch((z) => {
        this.pendingControlResponses.delete(X), Y(z);
      });
    });
  }
  initializationResult() {
    return this.initialization;
  }
  async supportedCommands() {
    return (await this.initialization).commands;
  }
  async supportedModels() {
    return (await this.initialization).models;
  }
  async supportedAgents() {
    return (await this.initialization).agents;
  }
  async reconnectMcpServer($) {
    await this.request({ subtype: "mcp_reconnect", serverName: $ });
  }
  async toggleMcpServer($, X) {
    await this.request({ subtype: "mcp_toggle", serverName: $, enabled: X });
  }
  async enableChannel($) {
    await this.request({ subtype: "channel_enable", serverName: $ });
  }
  async mcpAuthenticate($) {
    return (await this.request({ subtype: "mcp_authenticate", serverName: $ })).response;
  }
  async mcpClearAuth($) {
    return (await this.request({ subtype: "mcp_clear_auth", serverName: $ })).response;
  }
  async mcpSubmitOAuthCallbackUrl($, X) {
    return (await this.request({ subtype: "mcp_oauth_callback_url", serverName: $, callbackUrl: X })).response;
  }
  async claudeAuthenticate($) {
    return (await this.request({ subtype: "claude_authenticate", loginWithClaudeAi: $ })).response;
  }
  async claudeOAuthCallback($, X) {
    return (await this.request({ subtype: "claude_oauth_callback", authorizationCode: $, state: X })).response;
  }
  async claudeOAuthWaitForCompletion() {
    return (await this.request({ subtype: "claude_oauth_wait_for_completion" })).response;
  }
  async mcpServerStatus() {
    return (await this.request({ subtype: "mcp_status" })).response.mcpServers;
  }
  async getContextUsage() {
    return (await this.request({ subtype: "get_context_usage" })).response;
  }
  async reloadPlugins() {
    return (await this.request({ subtype: "reload_plugins" })).response;
  }
  async setMcpServers($) {
    let X = {}, J = {};
    for (let [G, U] of Object.entries($)) if (U.type === "sdk" && "instance" in U) X[G] = U.instance;
    else J[G] = U;
    let Q = new Set(this.sdkMcpServerInstances.keys()), Y = new Set(Object.keys(X));
    for (let G of Q) if (!Y.has(G)) await this.disconnectSdkMcpServer(G);
    for (let [G, U] of Object.entries(X)) if (!Q.has(G)) this.connectSdkMcpServer(G, U);
    let z = {};
    for (let G of Object.keys(X)) z[G] = { type: "sdk", name: G };
    return (await this.request({ subtype: "mcp_set_servers", servers: { ...J, ...z } })).response;
  }
  async accountInfo() {
    return (await this.initialization).account;
  }
  async streamInput($) {
    L6("[Query.streamInput] Starting to process input stream");
    try {
      let X = 0;
      for await (let J of $) {
        if (X++, L6(`[Query.streamInput] Processing message ${X}: ${J.type}`), this.abortController?.signal.aborted) break;
        await Promise.resolve(this.transport.write(q$(J) + `
`));
      }
      if (L6(`[Query.streamInput] Finished processing ${X} messages from input stream`), X > 0 && this.hasBidirectionalNeeds()) L6("[Query.streamInput] Has bidirectional needs, waiting for first result"), await this.waitForFirstResult();
      L6("[Query] Calling transport.endInput() to close stdin to CLI process"), this.transport.endInput();
    } catch (X) {
      if (!(X instanceof a$)) throw X;
    }
  }
  waitForFirstResult() {
    if (this.firstResultReceived) return L6("[Query.waitForFirstResult] Result already received, returning immediately"), Promise.resolve();
    return new Promise(($) => {
      if (this.abortController?.signal.aborted) {
        $();
        return;
      }
      this.abortController?.signal.addEventListener("abort", () => $(), { once: true }), this.firstResultReceivedResolve = $;
    });
  }
  handleHookCallbacks($, X, J, Q) {
    let Y = this.hookCallbacks.get($);
    if (!Y) throw Error(`No hook callback found for ID: ${$}`);
    return Y(X, J, { signal: Q });
  }
  connectSdkMcpServer($, X) {
    let J = new fz((Q) => this.sendMcpServerMessageToCli($, Q));
    this.sdkMcpTransports.set($, J), this.sdkMcpServerInstances.set($, X), X.connect(J).catch((Q) => {
      if (this.sdkMcpTransports.get($) === J) this.sdkMcpTransports.delete($);
      if (this.sdkMcpServerInstances.get($) === X) this.sdkMcpServerInstances.delete($);
      L6(`[Query.connectSdkMcpServer] Failed to connect MCP server '${$}': ${Q}`, { level: "error" });
    });
  }
  async disconnectSdkMcpServer($) {
    let X = this.sdkMcpTransports.get($);
    if (X) await X.close(), this.sdkMcpTransports.delete($);
    this.sdkMcpServerInstances.delete($);
  }
  sendMcpServerMessageToCli($, X) {
    if ("id" in X && X.id !== null && X.id !== void 0) {
      let Q = `${$}:${X.id}`, Y = this.pendingMcpResponses.get(Q);
      if (Y) {
        Y.resolve(X), this.pendingMcpResponses.delete(Q);
        return;
      }
    }
    let J = { type: "control_request", request_id: (0, import_crypto2.randomUUID)(), request: { subtype: "mcp_message", server_name: $, message: X } };
    Promise.resolve(this.transport.write(q$(J) + `
`)).catch((Q) => {
      L6(`[Query.sendMcpServerMessageToCli] Transport write failed: ${Q}`, { level: "error" });
    });
  }
  handleMcpControlRequest($, X, J) {
    let Q = "id" in X.message ? X.message.id : null, Y = `${$}:${Q}`;
    return new Promise((z, W) => {
      let G = () => {
        this.pendingMcpResponses.delete(Y);
      }, U = (K) => {
        G(), z(K);
      }, H = (K) => {
        G(), W(K);
      };
      if (this.pendingMcpResponses.set(Y, { resolve: U, reject: H }), J.onmessage) J.onmessage(X.message);
      else {
        G(), W(Error("No message handler registered"));
        return;
      }
    });
  }
};
var bM = (0, import_util.promisify)(import_child_process2.execFile);
var RJ = Buffer.from('{"type":"attribution-snapshot"');
var xM = Buffer.from('{"type":"system"');
var iX = 10;
var TM = Buffer.from([iX]);
var X$;
(function($) {
  $.assertEqual = (Y) => {
  };
  function X(Y) {
  }
  $.assertIs = X;
  function J(Y) {
    throw Error();
  }
  $.assertNever = J, $.arrayToEnum = (Y) => {
    let z = {};
    for (let W of Y) z[W] = W;
    return z;
  }, $.getValidEnumValues = (Y) => {
    let z = $.objectKeys(Y).filter((G) => typeof Y[Y[G]] !== "number"), W = {};
    for (let G of z) W[G] = Y[G];
    return $.objectValues(W);
  }, $.objectValues = (Y) => {
    return $.objectKeys(Y).map(function(z) {
      return Y[z];
    });
  }, $.objectKeys = typeof Object.keys === "function" ? (Y) => Object.keys(Y) : (Y) => {
    let z = [];
    for (let W in Y) if (Object.prototype.hasOwnProperty.call(Y, W)) z.push(W);
    return z;
  }, $.find = (Y, z) => {
    for (let W of Y) if (z(W)) return W;
    return;
  }, $.isInteger = typeof Number.isInteger === "function" ? (Y) => Number.isInteger(Y) : (Y) => typeof Y === "number" && Number.isFinite(Y) && Math.floor(Y) === Y;
  function Q(Y, z = " | ") {
    return Y.map((W) => typeof W === "string" ? `'${W}'` : W).join(z);
  }
  $.joinValues = Q, $.jsonStringifyReplacer = (Y, z) => {
    if (typeof z === "bigint") return z.toString();
    return z;
  };
})(X$ || (X$ = {}));
var sK;
(function($) {
  $.mergeShapes = (X, J) => {
    return { ...X, ...J };
  };
})(sK || (sK = {}));
var R = X$.arrayToEnum(["string", "nan", "number", "integer", "float", "boolean", "date", "bigint", "symbol", "function", "undefined", "null", "array", "object", "unknown", "promise", "void", "never", "map", "set"]);
var Y4 = ($) => {
  switch (typeof $) {
    case "undefined":
      return R.undefined;
    case "string":
      return R.string;
    case "number":
      return Number.isNaN($) ? R.nan : R.number;
    case "boolean":
      return R.boolean;
    case "function":
      return R.function;
    case "bigint":
      return R.bigint;
    case "symbol":
      return R.symbol;
    case "object":
      if (Array.isArray($)) return R.array;
      if ($ === null) return R.null;
      if ($.then && typeof $.then === "function" && $.catch && typeof $.catch === "function") return R.promise;
      if (typeof Map < "u" && $ instanceof Map) return R.map;
      if (typeof Set < "u" && $ instanceof Set) return R.set;
      if (typeof Date < "u" && $ instanceof Date) return R.date;
      return R.object;
    default:
      return R.unknown;
  }
};
var A = X$.arrayToEnum(["invalid_type", "invalid_literal", "custom", "invalid_union", "invalid_union_discriminator", "invalid_enum_value", "unrecognized_keys", "invalid_arguments", "invalid_return_type", "invalid_date", "invalid_string", "too_small", "too_big", "invalid_intersection_types", "not_multiple_of", "not_finite"]);
var V6 = class _V6 extends Error {
  get errors() {
    return this.issues;
  }
  constructor($) {
    super();
    this.issues = [], this.addIssue = (J) => {
      this.issues = [...this.issues, J];
    }, this.addIssues = (J = []) => {
      this.issues = [...this.issues, ...J];
    };
    let X = new.target.prototype;
    if (Object.setPrototypeOf) Object.setPrototypeOf(this, X);
    else this.__proto__ = X;
    this.name = "ZodError", this.issues = $;
  }
  format($) {
    let X = $ || function(Y) {
      return Y.message;
    }, J = { _errors: [] }, Q = (Y) => {
      for (let z of Y.issues) if (z.code === "invalid_union") z.unionErrors.map(Q);
      else if (z.code === "invalid_return_type") Q(z.returnTypeError);
      else if (z.code === "invalid_arguments") Q(z.argumentsError);
      else if (z.path.length === 0) J._errors.push(X(z));
      else {
        let W = J, G = 0;
        while (G < z.path.length) {
          let U = z.path[G];
          if (G !== z.path.length - 1) W[U] = W[U] || { _errors: [] };
          else W[U] = W[U] || { _errors: [] }, W[U]._errors.push(X(z));
          W = W[U], G++;
        }
      }
    };
    return Q(this), J;
  }
  static assert($) {
    if (!($ instanceof _V6)) throw Error(`Not a ZodError: ${$}`);
  }
  toString() {
    return this.message;
  }
  get message() {
    return JSON.stringify(this.issues, X$.jsonStringifyReplacer, 2);
  }
  get isEmpty() {
    return this.issues.length === 0;
  }
  flatten($ = (X) => X.message) {
    let X = {}, J = [];
    for (let Q of this.issues) if (Q.path.length > 0) {
      let Y = Q.path[0];
      X[Y] = X[Y] || [], X[Y].push($(Q));
    } else J.push($(Q));
    return { formErrors: J, fieldErrors: X };
  }
  get formErrors() {
    return this.flatten();
  }
};
V6.create = ($) => {
  return new V6($);
};
var qI = ($, X) => {
  let J;
  switch ($.code) {
    case A.invalid_type:
      if ($.received === R.undefined) J = "Required";
      else J = `Expected ${$.expected}, received ${$.received}`;
      break;
    case A.invalid_literal:
      J = `Invalid literal value, expected ${JSON.stringify($.expected, X$.jsonStringifyReplacer)}`;
      break;
    case A.unrecognized_keys:
      J = `Unrecognized key(s) in object: ${X$.joinValues($.keys, ", ")}`;
      break;
    case A.invalid_union:
      J = "Invalid input";
      break;
    case A.invalid_union_discriminator:
      J = `Invalid discriminator value. Expected ${X$.joinValues($.options)}`;
      break;
    case A.invalid_enum_value:
      J = `Invalid enum value. Expected ${X$.joinValues($.options)}, received '${$.received}'`;
      break;
    case A.invalid_arguments:
      J = "Invalid function arguments";
      break;
    case A.invalid_return_type:
      J = "Invalid function return type";
      break;
    case A.invalid_date:
      J = "Invalid date";
      break;
    case A.invalid_string:
      if (typeof $.validation === "object") if ("includes" in $.validation) {
        if (J = `Invalid input: must include "${$.validation.includes}"`, typeof $.validation.position === "number") J = `${J} at one or more positions greater than or equal to ${$.validation.position}`;
      } else if ("startsWith" in $.validation) J = `Invalid input: must start with "${$.validation.startsWith}"`;
      else if ("endsWith" in $.validation) J = `Invalid input: must end with "${$.validation.endsWith}"`;
      else X$.assertNever($.validation);
      else if ($.validation !== "regex") J = `Invalid ${$.validation}`;
      else J = "Invalid";
      break;
    case A.too_small:
      if ($.type === "array") J = `Array must contain ${$.exact ? "exactly" : $.inclusive ? "at least" : "more than"} ${$.minimum} element(s)`;
      else if ($.type === "string") J = `String must contain ${$.exact ? "exactly" : $.inclusive ? "at least" : "over"} ${$.minimum} character(s)`;
      else if ($.type === "number") J = `Number must be ${$.exact ? "exactly equal to " : $.inclusive ? "greater than or equal to " : "greater than "}${$.minimum}`;
      else if ($.type === "bigint") J = `Number must be ${$.exact ? "exactly equal to " : $.inclusive ? "greater than or equal to " : "greater than "}${$.minimum}`;
      else if ($.type === "date") J = `Date must be ${$.exact ? "exactly equal to " : $.inclusive ? "greater than or equal to " : "greater than "}${new Date(Number($.minimum))}`;
      else J = "Invalid input";
      break;
    case A.too_big:
      if ($.type === "array") J = `Array must contain ${$.exact ? "exactly" : $.inclusive ? "at most" : "less than"} ${$.maximum} element(s)`;
      else if ($.type === "string") J = `String must contain ${$.exact ? "exactly" : $.inclusive ? "at most" : "under"} ${$.maximum} character(s)`;
      else if ($.type === "number") J = `Number must be ${$.exact ? "exactly" : $.inclusive ? "less than or equal to" : "less than"} ${$.maximum}`;
      else if ($.type === "bigint") J = `BigInt must be ${$.exact ? "exactly" : $.inclusive ? "less than or equal to" : "less than"} ${$.maximum}`;
      else if ($.type === "date") J = `Date must be ${$.exact ? "exactly" : $.inclusive ? "smaller than or equal to" : "smaller than"} ${new Date(Number($.maximum))}`;
      else J = "Invalid input";
      break;
    case A.custom:
      J = "Invalid input";
      break;
    case A.invalid_intersection_types:
      J = "Intersection results could not be merged";
      break;
    case A.not_multiple_of:
      J = `Number must be a multiple of ${$.multipleOf}`;
      break;
    case A.not_finite:
      J = "Number must be finite";
      break;
    default:
      J = X.defaultError, X$.assertNever($);
  }
  return { message: J };
};
var S4 = qI;
var DI = S4;
function rX() {
  return DI;
}
var _J = ($) => {
  let { data: X, path: J, errorMaps: Q, issueData: Y } = $, z = [...J, ...Y.path || []], W = { ...Y, path: z };
  if (Y.message !== void 0) return { ...Y, path: z, message: Y.message };
  let G = "", U = Q.filter((H) => !!H).slice().reverse();
  for (let H of U) G = H(W, { data: X, defaultError: G }).message;
  return { ...Y, path: z, message: G };
};
function C($, X) {
  let J = rX(), Q = _J({ issueData: X, data: $.data, path: $.path, errorMaps: [$.common.contextualErrorMap, $.schemaErrorMap, J, J === S4 ? void 0 : S4].filter((Y) => !!Y) });
  $.common.issues.push(Q);
}
var u$ = class _u$ {
  constructor() {
    this.value = "valid";
  }
  dirty() {
    if (this.value === "valid") this.value = "dirty";
  }
  abort() {
    if (this.value !== "aborted") this.value = "aborted";
  }
  static mergeArray($, X) {
    let J = [];
    for (let Q of X) {
      if (Q.status === "aborted") return l;
      if (Q.status === "dirty") $.dirty();
      J.push(Q.value);
    }
    return { status: $.value, value: J };
  }
  static async mergeObjectAsync($, X) {
    let J = [];
    for (let Q of X) {
      let Y = await Q.key, z = await Q.value;
      J.push({ key: Y, value: z });
    }
    return _u$.mergeObjectSync($, J);
  }
  static mergeObjectSync($, X) {
    let J = {};
    for (let Q of X) {
      let { key: Y, value: z } = Q;
      if (Y.status === "aborted") return l;
      if (z.status === "aborted") return l;
      if (Y.status === "dirty") $.dirty();
      if (z.status === "dirty") $.dirty();
      if (Y.value !== "__proto__" && (typeof z.value < "u" || Q.alwaysSet)) J[Y.value] = z.value;
    }
    return { status: $.value, value: J };
  }
};
var l = Object.freeze({ status: "aborted" });
var z0 = ($) => ({ status: "dirty", value: $ });
var n$ = ($) => ({ status: "valid", value: $ });
var sz = ($) => $.status === "aborted";
var ez = ($) => $.status === "dirty";
var w1 = ($) => $.status === "valid";
var oX = ($) => typeof Promise < "u" && $ instanceof Promise;
var y;
(function($) {
  $.errToObj = (X) => typeof X === "string" ? { message: X } : X || {}, $.toString = (X) => typeof X === "string" ? X : X?.message;
})(y || (y = {}));
var v6 = class {
  constructor($, X, J, Q) {
    this._cachedPath = [], this.parent = $, this.data = X, this._path = J, this._key = Q;
  }
  get path() {
    if (!this._cachedPath.length) if (Array.isArray(this._key)) this._cachedPath.push(...this._path, ...this._key);
    else this._cachedPath.push(...this._path, this._key);
    return this._cachedPath;
  }
};
var eK = ($, X) => {
  if (w1(X)) return { success: true, data: X.value };
  else {
    if (!$.common.issues.length) throw Error("Validation failed but no issues detected.");
    return { success: false, get error() {
      if (this._error) return this._error;
      let J = new V6($.common.issues);
      return this._error = J, this._error;
    } };
  }
};
function o($) {
  if (!$) return {};
  let { errorMap: X, invalid_type_error: J, required_error: Q, description: Y } = $;
  if (X && (J || Q)) throw Error(`Can't use "invalid_type_error" or "required_error" in conjunction with custom error map.`);
  if (X) return { errorMap: X, description: Y };
  return { errorMap: (W, G) => {
    let { message: U } = $;
    if (W.code === "invalid_enum_value") return { message: U ?? G.defaultError };
    if (typeof G.data > "u") return { message: U ?? Q ?? G.defaultError };
    if (W.code !== "invalid_type") return { message: G.defaultError };
    return { message: U ?? J ?? G.defaultError };
  }, description: Y };
}
var e = class {
  get description() {
    return this._def.description;
  }
  _getType($) {
    return Y4($.data);
  }
  _getOrReturnCtx($, X) {
    return X || { common: $.parent.common, data: $.data, parsedType: Y4($.data), schemaErrorMap: this._def.errorMap, path: $.path, parent: $.parent };
  }
  _processInputParams($) {
    return { status: new u$(), ctx: { common: $.parent.common, data: $.data, parsedType: Y4($.data), schemaErrorMap: this._def.errorMap, path: $.path, parent: $.parent } };
  }
  _parseSync($) {
    let X = this._parse($);
    if (oX(X)) throw Error("Synchronous parse encountered promise.");
    return X;
  }
  _parseAsync($) {
    let X = this._parse($);
    return Promise.resolve(X);
  }
  parse($, X) {
    let J = this.safeParse($, X);
    if (J.success) return J.data;
    throw J.error;
  }
  safeParse($, X) {
    let J = { common: { issues: [], async: X?.async ?? false, contextualErrorMap: X?.errorMap }, path: X?.path || [], schemaErrorMap: this._def.errorMap, parent: null, data: $, parsedType: Y4($) }, Q = this._parseSync({ data: $, path: J.path, parent: J });
    return eK(J, Q);
  }
  "~validate"($) {
    let X = { common: { issues: [], async: !!this["~standard"].async }, path: [], schemaErrorMap: this._def.errorMap, parent: null, data: $, parsedType: Y4($) };
    if (!this["~standard"].async) try {
      let J = this._parseSync({ data: $, path: [], parent: X });
      return w1(J) ? { value: J.value } : { issues: X.common.issues };
    } catch (J) {
      if (J?.message?.toLowerCase()?.includes("encountered")) this["~standard"].async = true;
      X.common = { issues: [], async: true };
    }
    return this._parseAsync({ data: $, path: [], parent: X }).then((J) => w1(J) ? { value: J.value } : { issues: X.common.issues });
  }
  async parseAsync($, X) {
    let J = await this.safeParseAsync($, X);
    if (J.success) return J.data;
    throw J.error;
  }
  async safeParseAsync($, X) {
    let J = { common: { issues: [], contextualErrorMap: X?.errorMap, async: true }, path: X?.path || [], schemaErrorMap: this._def.errorMap, parent: null, data: $, parsedType: Y4($) }, Q = this._parse({ data: $, path: J.path, parent: J }), Y = await (oX(Q) ? Q : Promise.resolve(Q));
    return eK(J, Y);
  }
  refine($, X) {
    let J = (Q) => {
      if (typeof X === "string" || typeof X > "u") return { message: X };
      else if (typeof X === "function") return X(Q);
      else return X;
    };
    return this._refinement((Q, Y) => {
      let z = $(Q), W = () => Y.addIssue({ code: A.custom, ...J(Q) });
      if (typeof Promise < "u" && z instanceof Promise) return z.then((G) => {
        if (!G) return W(), false;
        else return true;
      });
      if (!z) return W(), false;
      else return true;
    });
  }
  refinement($, X) {
    return this._refinement((J, Q) => {
      if (!$(J)) return Q.addIssue(typeof X === "function" ? X(J, Q) : X), false;
      else return true;
    });
  }
  _refinement($) {
    return new p6({ schema: this, typeName: P.ZodEffects, effect: { type: "refinement", refinement: $ } });
  }
  superRefine($) {
    return this._refinement($);
  }
  constructor($) {
    this.spa = this.safeParseAsync, this._def = $, this.parse = this.parse.bind(this), this.safeParse = this.safeParse.bind(this), this.parseAsync = this.parseAsync.bind(this), this.safeParseAsync = this.safeParseAsync.bind(this), this.spa = this.spa.bind(this), this.refine = this.refine.bind(this), this.refinement = this.refinement.bind(this), this.superRefine = this.superRefine.bind(this), this.optional = this.optional.bind(this), this.nullable = this.nullable.bind(this), this.nullish = this.nullish.bind(this), this.array = this.array.bind(this), this.promise = this.promise.bind(this), this.or = this.or.bind(this), this.and = this.and.bind(this), this.transform = this.transform.bind(this), this.brand = this.brand.bind(this), this.default = this.default.bind(this), this.catch = this.catch.bind(this), this.describe = this.describe.bind(this), this.pipe = this.pipe.bind(this), this.readonly = this.readonly.bind(this), this.isNullable = this.isNullable.bind(this), this.isOptional = this.isOptional.bind(this), this["~standard"] = { version: 1, vendor: "zod", validate: (X) => this["~validate"](X) };
  }
  optional() {
    return M6.create(this, this._def);
  }
  nullable() {
    return v4.create(this, this._def);
  }
  nullish() {
    return this.nullable().optional();
  }
  array() {
    return c6.create(this);
  }
  promise() {
    return K0.create(this, this._def);
  }
  or($) {
    return $8.create([this, $], this._def);
  }
  and($) {
    return X8.create(this, $, this._def);
  }
  transform($) {
    return new p6({ ...o(this._def), schema: this, typeName: P.ZodEffects, effect: { type: "transform", transform: $ } });
  }
  default($) {
    let X = typeof $ === "function" ? $ : () => $;
    return new z8({ ...o(this._def), innerType: this, defaultValue: X, typeName: P.ZodDefault });
  }
  brand() {
    return new Y5({ typeName: P.ZodBranded, type: this, ...o(this._def) });
  }
  catch($) {
    let X = typeof $ === "function" ? $ : () => $;
    return new W8({ ...o(this._def), innerType: this, catchValue: X, typeName: P.ZodCatch });
  }
  describe($) {
    return new this.constructor({ ...this._def, description: $ });
  }
  pipe($) {
    return mJ.create(this, $);
  }
  readonly() {
    return G8.create(this);
  }
  isOptional() {
    return this.safeParse(void 0).success;
  }
  isNullable() {
    return this.safeParse(null).success;
  }
};
var LI = /^c[^\s-]{8,}$/i;
var jI = /^[0-9a-z]+$/;
var FI = /^[0-9A-HJKMNP-TV-Z]{26}$/i;
var MI = /^[0-9a-fA-F]{8}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{12}$/i;
var II = /^[a-z0-9_-]{21}$/i;
var AI = /^[A-Za-z0-9-_]+\.[A-Za-z0-9-_]+\.[A-Za-z0-9-_]*$/;
var bI = /^[-+]?P(?!$)(?:(?:[-+]?\d+Y)|(?:[-+]?\d+[.,]\d+Y$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:(?:[-+]?\d+W)|(?:[-+]?\d+[.,]\d+W$))?(?:(?:[-+]?\d+D)|(?:[-+]?\d+[.,]\d+D$))?(?:T(?=[\d+-])(?:(?:[-+]?\d+H)|(?:[-+]?\d+[.,]\d+H$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:[-+]?\d+(?:[.,]\d+)?S)?)??$/;
var PI = /^(?!\.)(?!.*\.\.)([A-Z0-9_'+\-\.]*)[A-Z0-9_+-]@([A-Z0-9][A-Z0-9\-]*\.)+[A-Z]{2,}$/i;
var ZI = "^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$";
var $5;
var EI = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$/;
var RI = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\/(3[0-2]|[12]?[0-9])$/;
var SI = /^(([0-9a-fA-F]{1,4}:){7,7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))$/;
var vI = /^(([0-9a-fA-F]{1,4}:){7,7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))\/(12[0-8]|1[01][0-9]|[1-9]?[0-9])$/;
var CI = /^([0-9a-zA-Z+/]{4})*(([0-9a-zA-Z+/]{2}==)|([0-9a-zA-Z+/]{3}=))?$/;
var kI = /^([0-9a-zA-Z-_]{4})*(([0-9a-zA-Z-_]{2}(==)?)|([0-9a-zA-Z-_]{3}(=)?))?$/;
var $N = "((\\d\\d[2468][048]|\\d\\d[13579][26]|\\d\\d0[48]|[02468][048]00|[13579][26]00)-02-29|\\d{4}-((0[13578]|1[02])-(0[1-9]|[12]\\d|3[01])|(0[469]|11)-(0[1-9]|[12]\\d|30)|(02)-(0[1-9]|1\\d|2[0-8])))";
var _I = new RegExp(`^${$N}$`);
function XN($) {
  let X = "[0-5]\\d";
  if ($.precision) X = `${X}\\.\\d{${$.precision}}`;
  else if ($.precision == null) X = `${X}(\\.\\d+)?`;
  let J = $.precision ? "+" : "?";
  return `([01]\\d|2[0-3]):[0-5]\\d(:${X})${J}`;
}
function xI($) {
  return new RegExp(`^${XN($)}$`);
}
function TI($) {
  let X = `${$N}T${XN($)}`, J = [];
  if (J.push($.local ? "Z?" : "Z"), $.offset) J.push("([+-]\\d{2}:?\\d{2})");
  return X = `${X}(${J.join("|")})`, new RegExp(`^${X}$`);
}
function yI($, X) {
  if ((X === "v4" || !X) && EI.test($)) return true;
  if ((X === "v6" || !X) && SI.test($)) return true;
  return false;
}
function fI($, X) {
  if (!AI.test($)) return false;
  try {
    let [J] = $.split(".");
    if (!J) return false;
    let Q = J.replace(/-/g, "+").replace(/_/g, "/").padEnd(J.length + (4 - J.length % 4) % 4, "="), Y = JSON.parse(atob(Q));
    if (typeof Y !== "object" || Y === null) return false;
    if ("typ" in Y && Y?.typ !== "JWT") return false;
    if (!Y.alg) return false;
    if (X && Y.alg !== X) return false;
    return true;
  } catch {
    return false;
  }
}
function gI($, X) {
  if ((X === "v4" || !X) && RI.test($)) return true;
  if ((X === "v6" || !X) && vI.test($)) return true;
  return false;
}
var z4 = class _z4 extends e {
  _parse($) {
    if (this._def.coerce) $.data = String($.data);
    if (this._getType($) !== R.string) {
      let Y = this._getOrReturnCtx($);
      return C(Y, { code: A.invalid_type, expected: R.string, received: Y.parsedType }), l;
    }
    let J = new u$(), Q = void 0;
    for (let Y of this._def.checks) if (Y.kind === "min") {
      if ($.data.length < Y.value) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.too_small, minimum: Y.value, type: "string", inclusive: true, exact: false, message: Y.message }), J.dirty();
    } else if (Y.kind === "max") {
      if ($.data.length > Y.value) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.too_big, maximum: Y.value, type: "string", inclusive: true, exact: false, message: Y.message }), J.dirty();
    } else if (Y.kind === "length") {
      let z = $.data.length > Y.value, W = $.data.length < Y.value;
      if (z || W) {
        if (Q = this._getOrReturnCtx($, Q), z) C(Q, { code: A.too_big, maximum: Y.value, type: "string", inclusive: true, exact: true, message: Y.message });
        else if (W) C(Q, { code: A.too_small, minimum: Y.value, type: "string", inclusive: true, exact: true, message: Y.message });
        J.dirty();
      }
    } else if (Y.kind === "email") {
      if (!PI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "email", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "emoji") {
      if (!$5) $5 = new RegExp(ZI, "u");
      if (!$5.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "emoji", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "uuid") {
      if (!MI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "uuid", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "nanoid") {
      if (!II.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "nanoid", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "cuid") {
      if (!LI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "cuid", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "cuid2") {
      if (!jI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "cuid2", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "ulid") {
      if (!FI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "ulid", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "url") try {
      new URL($.data);
    } catch {
      Q = this._getOrReturnCtx($, Q), C(Q, { validation: "url", code: A.invalid_string, message: Y.message }), J.dirty();
    }
    else if (Y.kind === "regex") {
      if (Y.regex.lastIndex = 0, !Y.regex.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "regex", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "trim") $.data = $.data.trim();
    else if (Y.kind === "includes") {
      if (!$.data.includes(Y.value, Y.position)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: { includes: Y.value, position: Y.position }, message: Y.message }), J.dirty();
    } else if (Y.kind === "toLowerCase") $.data = $.data.toLowerCase();
    else if (Y.kind === "toUpperCase") $.data = $.data.toUpperCase();
    else if (Y.kind === "startsWith") {
      if (!$.data.startsWith(Y.value)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: { startsWith: Y.value }, message: Y.message }), J.dirty();
    } else if (Y.kind === "endsWith") {
      if (!$.data.endsWith(Y.value)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: { endsWith: Y.value }, message: Y.message }), J.dirty();
    } else if (Y.kind === "datetime") {
      if (!TI(Y).test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: "datetime", message: Y.message }), J.dirty();
    } else if (Y.kind === "date") {
      if (!_I.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: "date", message: Y.message }), J.dirty();
    } else if (Y.kind === "time") {
      if (!xI(Y).test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.invalid_string, validation: "time", message: Y.message }), J.dirty();
    } else if (Y.kind === "duration") {
      if (!bI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "duration", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "ip") {
      if (!yI($.data, Y.version)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "ip", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "jwt") {
      if (!fI($.data, Y.alg)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "jwt", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "cidr") {
      if (!gI($.data, Y.version)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "cidr", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "base64") {
      if (!CI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "base64", code: A.invalid_string, message: Y.message }), J.dirty();
    } else if (Y.kind === "base64url") {
      if (!kI.test($.data)) Q = this._getOrReturnCtx($, Q), C(Q, { validation: "base64url", code: A.invalid_string, message: Y.message }), J.dirty();
    } else X$.assertNever(Y);
    return { status: J.value, value: $.data };
  }
  _regex($, X, J) {
    return this.refinement((Q) => $.test(Q), { validation: X, code: A.invalid_string, ...y.errToObj(J) });
  }
  _addCheck($) {
    return new _z4({ ...this._def, checks: [...this._def.checks, $] });
  }
  email($) {
    return this._addCheck({ kind: "email", ...y.errToObj($) });
  }
  url($) {
    return this._addCheck({ kind: "url", ...y.errToObj($) });
  }
  emoji($) {
    return this._addCheck({ kind: "emoji", ...y.errToObj($) });
  }
  uuid($) {
    return this._addCheck({ kind: "uuid", ...y.errToObj($) });
  }
  nanoid($) {
    return this._addCheck({ kind: "nanoid", ...y.errToObj($) });
  }
  cuid($) {
    return this._addCheck({ kind: "cuid", ...y.errToObj($) });
  }
  cuid2($) {
    return this._addCheck({ kind: "cuid2", ...y.errToObj($) });
  }
  ulid($) {
    return this._addCheck({ kind: "ulid", ...y.errToObj($) });
  }
  base64($) {
    return this._addCheck({ kind: "base64", ...y.errToObj($) });
  }
  base64url($) {
    return this._addCheck({ kind: "base64url", ...y.errToObj($) });
  }
  jwt($) {
    return this._addCheck({ kind: "jwt", ...y.errToObj($) });
  }
  ip($) {
    return this._addCheck({ kind: "ip", ...y.errToObj($) });
  }
  cidr($) {
    return this._addCheck({ kind: "cidr", ...y.errToObj($) });
  }
  datetime($) {
    if (typeof $ === "string") return this._addCheck({ kind: "datetime", precision: null, offset: false, local: false, message: $ });
    return this._addCheck({ kind: "datetime", precision: typeof $?.precision > "u" ? null : $?.precision, offset: $?.offset ?? false, local: $?.local ?? false, ...y.errToObj($?.message) });
  }
  date($) {
    return this._addCheck({ kind: "date", message: $ });
  }
  time($) {
    if (typeof $ === "string") return this._addCheck({ kind: "time", precision: null, message: $ });
    return this._addCheck({ kind: "time", precision: typeof $?.precision > "u" ? null : $?.precision, ...y.errToObj($?.message) });
  }
  duration($) {
    return this._addCheck({ kind: "duration", ...y.errToObj($) });
  }
  regex($, X) {
    return this._addCheck({ kind: "regex", regex: $, ...y.errToObj(X) });
  }
  includes($, X) {
    return this._addCheck({ kind: "includes", value: $, position: X?.position, ...y.errToObj(X?.message) });
  }
  startsWith($, X) {
    return this._addCheck({ kind: "startsWith", value: $, ...y.errToObj(X) });
  }
  endsWith($, X) {
    return this._addCheck({ kind: "endsWith", value: $, ...y.errToObj(X) });
  }
  min($, X) {
    return this._addCheck({ kind: "min", value: $, ...y.errToObj(X) });
  }
  max($, X) {
    return this._addCheck({ kind: "max", value: $, ...y.errToObj(X) });
  }
  length($, X) {
    return this._addCheck({ kind: "length", value: $, ...y.errToObj(X) });
  }
  nonempty($) {
    return this.min(1, y.errToObj($));
  }
  trim() {
    return new _z4({ ...this._def, checks: [...this._def.checks, { kind: "trim" }] });
  }
  toLowerCase() {
    return new _z4({ ...this._def, checks: [...this._def.checks, { kind: "toLowerCase" }] });
  }
  toUpperCase() {
    return new _z4({ ...this._def, checks: [...this._def.checks, { kind: "toUpperCase" }] });
  }
  get isDatetime() {
    return !!this._def.checks.find(($) => $.kind === "datetime");
  }
  get isDate() {
    return !!this._def.checks.find(($) => $.kind === "date");
  }
  get isTime() {
    return !!this._def.checks.find(($) => $.kind === "time");
  }
  get isDuration() {
    return !!this._def.checks.find(($) => $.kind === "duration");
  }
  get isEmail() {
    return !!this._def.checks.find(($) => $.kind === "email");
  }
  get isURL() {
    return !!this._def.checks.find(($) => $.kind === "url");
  }
  get isEmoji() {
    return !!this._def.checks.find(($) => $.kind === "emoji");
  }
  get isUUID() {
    return !!this._def.checks.find(($) => $.kind === "uuid");
  }
  get isNANOID() {
    return !!this._def.checks.find(($) => $.kind === "nanoid");
  }
  get isCUID() {
    return !!this._def.checks.find(($) => $.kind === "cuid");
  }
  get isCUID2() {
    return !!this._def.checks.find(($) => $.kind === "cuid2");
  }
  get isULID() {
    return !!this._def.checks.find(($) => $.kind === "ulid");
  }
  get isIP() {
    return !!this._def.checks.find(($) => $.kind === "ip");
  }
  get isCIDR() {
    return !!this._def.checks.find(($) => $.kind === "cidr");
  }
  get isBase64() {
    return !!this._def.checks.find(($) => $.kind === "base64");
  }
  get isBase64url() {
    return !!this._def.checks.find(($) => $.kind === "base64url");
  }
  get minLength() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "min") {
      if ($ === null || X.value > $) $ = X.value;
    }
    return $;
  }
  get maxLength() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "max") {
      if ($ === null || X.value < $) $ = X.value;
    }
    return $;
  }
};
z4.create = ($) => {
  return new z4({ checks: [], typeName: P.ZodString, coerce: $?.coerce ?? false, ...o($) });
};
function hI($, X) {
  let J = ($.toString().split(".")[1] || "").length, Q = (X.toString().split(".")[1] || "").length, Y = J > Q ? J : Q, z = Number.parseInt($.toFixed(Y).replace(".", "")), W = Number.parseInt(X.toFixed(Y).replace(".", ""));
  return z % W / 10 ** Y;
}
var G0 = class _G0 extends e {
  constructor() {
    super(...arguments);
    this.min = this.gte, this.max = this.lte, this.step = this.multipleOf;
  }
  _parse($) {
    if (this._def.coerce) $.data = Number($.data);
    if (this._getType($) !== R.number) {
      let Y = this._getOrReturnCtx($);
      return C(Y, { code: A.invalid_type, expected: R.number, received: Y.parsedType }), l;
    }
    let J = void 0, Q = new u$();
    for (let Y of this._def.checks) if (Y.kind === "int") {
      if (!X$.isInteger($.data)) J = this._getOrReturnCtx($, J), C(J, { code: A.invalid_type, expected: "integer", received: "float", message: Y.message }), Q.dirty();
    } else if (Y.kind === "min") {
      if (Y.inclusive ? $.data < Y.value : $.data <= Y.value) J = this._getOrReturnCtx($, J), C(J, { code: A.too_small, minimum: Y.value, type: "number", inclusive: Y.inclusive, exact: false, message: Y.message }), Q.dirty();
    } else if (Y.kind === "max") {
      if (Y.inclusive ? $.data > Y.value : $.data >= Y.value) J = this._getOrReturnCtx($, J), C(J, { code: A.too_big, maximum: Y.value, type: "number", inclusive: Y.inclusive, exact: false, message: Y.message }), Q.dirty();
    } else if (Y.kind === "multipleOf") {
      if (hI($.data, Y.value) !== 0) J = this._getOrReturnCtx($, J), C(J, { code: A.not_multiple_of, multipleOf: Y.value, message: Y.message }), Q.dirty();
    } else if (Y.kind === "finite") {
      if (!Number.isFinite($.data)) J = this._getOrReturnCtx($, J), C(J, { code: A.not_finite, message: Y.message }), Q.dirty();
    } else X$.assertNever(Y);
    return { status: Q.value, value: $.data };
  }
  gte($, X) {
    return this.setLimit("min", $, true, y.toString(X));
  }
  gt($, X) {
    return this.setLimit("min", $, false, y.toString(X));
  }
  lte($, X) {
    return this.setLimit("max", $, true, y.toString(X));
  }
  lt($, X) {
    return this.setLimit("max", $, false, y.toString(X));
  }
  setLimit($, X, J, Q) {
    return new _G0({ ...this._def, checks: [...this._def.checks, { kind: $, value: X, inclusive: J, message: y.toString(Q) }] });
  }
  _addCheck($) {
    return new _G0({ ...this._def, checks: [...this._def.checks, $] });
  }
  int($) {
    return this._addCheck({ kind: "int", message: y.toString($) });
  }
  positive($) {
    return this._addCheck({ kind: "min", value: 0, inclusive: false, message: y.toString($) });
  }
  negative($) {
    return this._addCheck({ kind: "max", value: 0, inclusive: false, message: y.toString($) });
  }
  nonpositive($) {
    return this._addCheck({ kind: "max", value: 0, inclusive: true, message: y.toString($) });
  }
  nonnegative($) {
    return this._addCheck({ kind: "min", value: 0, inclusive: true, message: y.toString($) });
  }
  multipleOf($, X) {
    return this._addCheck({ kind: "multipleOf", value: $, message: y.toString(X) });
  }
  finite($) {
    return this._addCheck({ kind: "finite", message: y.toString($) });
  }
  safe($) {
    return this._addCheck({ kind: "min", inclusive: true, value: Number.MIN_SAFE_INTEGER, message: y.toString($) })._addCheck({ kind: "max", inclusive: true, value: Number.MAX_SAFE_INTEGER, message: y.toString($) });
  }
  get minValue() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "min") {
      if ($ === null || X.value > $) $ = X.value;
    }
    return $;
  }
  get maxValue() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "max") {
      if ($ === null || X.value < $) $ = X.value;
    }
    return $;
  }
  get isInt() {
    return !!this._def.checks.find(($) => $.kind === "int" || $.kind === "multipleOf" && X$.isInteger($.value));
  }
  get isFinite() {
    let $ = null, X = null;
    for (let J of this._def.checks) if (J.kind === "finite" || J.kind === "int" || J.kind === "multipleOf") return true;
    else if (J.kind === "min") {
      if (X === null || J.value > X) X = J.value;
    } else if (J.kind === "max") {
      if ($ === null || J.value < $) $ = J.value;
    }
    return Number.isFinite(X) && Number.isFinite($);
  }
};
G0.create = ($) => {
  return new G0({ checks: [], typeName: P.ZodNumber, coerce: $?.coerce || false, ...o($) });
};
var U0 = class _U0 extends e {
  constructor() {
    super(...arguments);
    this.min = this.gte, this.max = this.lte;
  }
  _parse($) {
    if (this._def.coerce) try {
      $.data = BigInt($.data);
    } catch {
      return this._getInvalidInput($);
    }
    if (this._getType($) !== R.bigint) return this._getInvalidInput($);
    let J = void 0, Q = new u$();
    for (let Y of this._def.checks) if (Y.kind === "min") {
      if (Y.inclusive ? $.data < Y.value : $.data <= Y.value) J = this._getOrReturnCtx($, J), C(J, { code: A.too_small, type: "bigint", minimum: Y.value, inclusive: Y.inclusive, message: Y.message }), Q.dirty();
    } else if (Y.kind === "max") {
      if (Y.inclusive ? $.data > Y.value : $.data >= Y.value) J = this._getOrReturnCtx($, J), C(J, { code: A.too_big, type: "bigint", maximum: Y.value, inclusive: Y.inclusive, message: Y.message }), Q.dirty();
    } else if (Y.kind === "multipleOf") {
      if ($.data % Y.value !== BigInt(0)) J = this._getOrReturnCtx($, J), C(J, { code: A.not_multiple_of, multipleOf: Y.value, message: Y.message }), Q.dirty();
    } else X$.assertNever(Y);
    return { status: Q.value, value: $.data };
  }
  _getInvalidInput($) {
    let X = this._getOrReturnCtx($);
    return C(X, { code: A.invalid_type, expected: R.bigint, received: X.parsedType }), l;
  }
  gte($, X) {
    return this.setLimit("min", $, true, y.toString(X));
  }
  gt($, X) {
    return this.setLimit("min", $, false, y.toString(X));
  }
  lte($, X) {
    return this.setLimit("max", $, true, y.toString(X));
  }
  lt($, X) {
    return this.setLimit("max", $, false, y.toString(X));
  }
  setLimit($, X, J, Q) {
    return new _U0({ ...this._def, checks: [...this._def.checks, { kind: $, value: X, inclusive: J, message: y.toString(Q) }] });
  }
  _addCheck($) {
    return new _U0({ ...this._def, checks: [...this._def.checks, $] });
  }
  positive($) {
    return this._addCheck({ kind: "min", value: BigInt(0), inclusive: false, message: y.toString($) });
  }
  negative($) {
    return this._addCheck({ kind: "max", value: BigInt(0), inclusive: false, message: y.toString($) });
  }
  nonpositive($) {
    return this._addCheck({ kind: "max", value: BigInt(0), inclusive: true, message: y.toString($) });
  }
  nonnegative($) {
    return this._addCheck({ kind: "min", value: BigInt(0), inclusive: true, message: y.toString($) });
  }
  multipleOf($, X) {
    return this._addCheck({ kind: "multipleOf", value: $, message: y.toString(X) });
  }
  get minValue() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "min") {
      if ($ === null || X.value > $) $ = X.value;
    }
    return $;
  }
  get maxValue() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "max") {
      if ($ === null || X.value < $) $ = X.value;
    }
    return $;
  }
};
U0.create = ($) => {
  return new U0({ checks: [], typeName: P.ZodBigInt, coerce: $?.coerce ?? false, ...o($) });
};
var xJ = class extends e {
  _parse($) {
    if (this._def.coerce) $.data = Boolean($.data);
    if (this._getType($) !== R.boolean) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.boolean, received: J.parsedType }), l;
    }
    return n$($.data);
  }
};
xJ.create = ($) => {
  return new xJ({ typeName: P.ZodBoolean, coerce: $?.coerce || false, ...o($) });
};
var aX = class _aX extends e {
  _parse($) {
    if (this._def.coerce) $.data = new Date($.data);
    if (this._getType($) !== R.date) {
      let Y = this._getOrReturnCtx($);
      return C(Y, { code: A.invalid_type, expected: R.date, received: Y.parsedType }), l;
    }
    if (Number.isNaN($.data.getTime())) {
      let Y = this._getOrReturnCtx($);
      return C(Y, { code: A.invalid_date }), l;
    }
    let J = new u$(), Q = void 0;
    for (let Y of this._def.checks) if (Y.kind === "min") {
      if ($.data.getTime() < Y.value) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.too_small, message: Y.message, inclusive: true, exact: false, minimum: Y.value, type: "date" }), J.dirty();
    } else if (Y.kind === "max") {
      if ($.data.getTime() > Y.value) Q = this._getOrReturnCtx($, Q), C(Q, { code: A.too_big, message: Y.message, inclusive: true, exact: false, maximum: Y.value, type: "date" }), J.dirty();
    } else X$.assertNever(Y);
    return { status: J.value, value: new Date($.data.getTime()) };
  }
  _addCheck($) {
    return new _aX({ ...this._def, checks: [...this._def.checks, $] });
  }
  min($, X) {
    return this._addCheck({ kind: "min", value: $.getTime(), message: y.toString(X) });
  }
  max($, X) {
    return this._addCheck({ kind: "max", value: $.getTime(), message: y.toString(X) });
  }
  get minDate() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "min") {
      if ($ === null || X.value > $) $ = X.value;
    }
    return $ != null ? new Date($) : null;
  }
  get maxDate() {
    let $ = null;
    for (let X of this._def.checks) if (X.kind === "max") {
      if ($ === null || X.value < $) $ = X.value;
    }
    return $ != null ? new Date($) : null;
  }
};
aX.create = ($) => {
  return new aX({ checks: [], coerce: $?.coerce || false, typeName: P.ZodDate, ...o($) });
};
var TJ = class extends e {
  _parse($) {
    if (this._getType($) !== R.symbol) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.symbol, received: J.parsedType }), l;
    }
    return n$($.data);
  }
};
TJ.create = ($) => {
  return new TJ({ typeName: P.ZodSymbol, ...o($) });
};
var sX = class extends e {
  _parse($) {
    if (this._getType($) !== R.undefined) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.undefined, received: J.parsedType }), l;
    }
    return n$($.data);
  }
};
sX.create = ($) => {
  return new sX({ typeName: P.ZodUndefined, ...o($) });
};
var eX = class extends e {
  _parse($) {
    if (this._getType($) !== R.null) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.null, received: J.parsedType }), l;
    }
    return n$($.data);
  }
};
eX.create = ($) => {
  return new eX({ typeName: P.ZodNull, ...o($) });
};
var yJ = class extends e {
  constructor() {
    super(...arguments);
    this._any = true;
  }
  _parse($) {
    return n$($.data);
  }
};
yJ.create = ($) => {
  return new yJ({ typeName: P.ZodAny, ...o($) });
};
var B1 = class extends e {
  constructor() {
    super(...arguments);
    this._unknown = true;
  }
  _parse($) {
    return n$($.data);
  }
};
B1.create = ($) => {
  return new B1({ typeName: P.ZodUnknown, ...o($) });
};
var W4 = class extends e {
  _parse($) {
    let X = this._getOrReturnCtx($);
    return C(X, { code: A.invalid_type, expected: R.never, received: X.parsedType }), l;
  }
};
W4.create = ($) => {
  return new W4({ typeName: P.ZodNever, ...o($) });
};
var fJ = class extends e {
  _parse($) {
    if (this._getType($) !== R.undefined) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.void, received: J.parsedType }), l;
    }
    return n$($.data);
  }
};
fJ.create = ($) => {
  return new fJ({ typeName: P.ZodVoid, ...o($) });
};
var c6 = class _c6 extends e {
  _parse($) {
    let { ctx: X, status: J } = this._processInputParams($), Q = this._def;
    if (X.parsedType !== R.array) return C(X, { code: A.invalid_type, expected: R.array, received: X.parsedType }), l;
    if (Q.exactLength !== null) {
      let z = X.data.length > Q.exactLength.value, W = X.data.length < Q.exactLength.value;
      if (z || W) C(X, { code: z ? A.too_big : A.too_small, minimum: W ? Q.exactLength.value : void 0, maximum: z ? Q.exactLength.value : void 0, type: "array", inclusive: true, exact: true, message: Q.exactLength.message }), J.dirty();
    }
    if (Q.minLength !== null) {
      if (X.data.length < Q.minLength.value) C(X, { code: A.too_small, minimum: Q.minLength.value, type: "array", inclusive: true, exact: false, message: Q.minLength.message }), J.dirty();
    }
    if (Q.maxLength !== null) {
      if (X.data.length > Q.maxLength.value) C(X, { code: A.too_big, maximum: Q.maxLength.value, type: "array", inclusive: true, exact: false, message: Q.maxLength.message }), J.dirty();
    }
    if (X.common.async) return Promise.all([...X.data].map((z, W) => {
      return Q.type._parseAsync(new v6(X, z, X.path, W));
    })).then((z) => {
      return u$.mergeArray(J, z);
    });
    let Y = [...X.data].map((z, W) => {
      return Q.type._parseSync(new v6(X, z, X.path, W));
    });
    return u$.mergeArray(J, Y);
  }
  get element() {
    return this._def.type;
  }
  min($, X) {
    return new _c6({ ...this._def, minLength: { value: $, message: y.toString(X) } });
  }
  max($, X) {
    return new _c6({ ...this._def, maxLength: { value: $, message: y.toString(X) } });
  }
  length($, X) {
    return new _c6({ ...this._def, exactLength: { value: $, message: y.toString(X) } });
  }
  nonempty($) {
    return this.min(1, $);
  }
};
c6.create = ($, X) => {
  return new c6({ type: $, minLength: null, maxLength: null, exactLength: null, typeName: P.ZodArray, ...o(X) });
};
function W0($) {
  if ($ instanceof Z$) {
    let X = {};
    for (let J in $.shape) {
      let Q = $.shape[J];
      X[J] = M6.create(W0(Q));
    }
    return new Z$({ ...$._def, shape: () => X });
  } else if ($ instanceof c6) return new c6({ ...$._def, type: W0($.element) });
  else if ($ instanceof M6) return M6.create(W0($.unwrap()));
  else if ($ instanceof v4) return v4.create(W0($.unwrap()));
  else if ($ instanceof G4) return G4.create($.items.map((X) => W0(X)));
  else return $;
}
var Z$ = class _Z$ extends e {
  constructor() {
    super(...arguments);
    this._cached = null, this.nonstrict = this.passthrough, this.augment = this.extend;
  }
  _getCached() {
    if (this._cached !== null) return this._cached;
    let $ = this._def.shape(), X = X$.objectKeys($);
    return this._cached = { shape: $, keys: X }, this._cached;
  }
  _parse($) {
    if (this._getType($) !== R.object) {
      let U = this._getOrReturnCtx($);
      return C(U, { code: A.invalid_type, expected: R.object, received: U.parsedType }), l;
    }
    let { status: J, ctx: Q } = this._processInputParams($), { shape: Y, keys: z } = this._getCached(), W = [];
    if (!(this._def.catchall instanceof W4 && this._def.unknownKeys === "strip")) {
      for (let U in Q.data) if (!z.includes(U)) W.push(U);
    }
    let G = [];
    for (let U of z) {
      let H = Y[U], K = Q.data[U];
      G.push({ key: { status: "valid", value: U }, value: H._parse(new v6(Q, K, Q.path, U)), alwaysSet: U in Q.data });
    }
    if (this._def.catchall instanceof W4) {
      let U = this._def.unknownKeys;
      if (U === "passthrough") for (let H of W) G.push({ key: { status: "valid", value: H }, value: { status: "valid", value: Q.data[H] } });
      else if (U === "strict") {
        if (W.length > 0) C(Q, { code: A.unrecognized_keys, keys: W }), J.dirty();
      } else if (U === "strip") ;
      else throw Error("Internal ZodObject error: invalid unknownKeys value.");
    } else {
      let U = this._def.catchall;
      for (let H of W) {
        let K = Q.data[H];
        G.push({ key: { status: "valid", value: H }, value: U._parse(new v6(Q, K, Q.path, H)), alwaysSet: H in Q.data });
      }
    }
    if (Q.common.async) return Promise.resolve().then(async () => {
      let U = [];
      for (let H of G) {
        let K = await H.key, V = await H.value;
        U.push({ key: K, value: V, alwaysSet: H.alwaysSet });
      }
      return U;
    }).then((U) => {
      return u$.mergeObjectSync(J, U);
    });
    else return u$.mergeObjectSync(J, G);
  }
  get shape() {
    return this._def.shape();
  }
  strict($) {
    return y.errToObj, new _Z$({ ...this._def, unknownKeys: "strict", ...$ !== void 0 ? { errorMap: (X, J) => {
      let Q = this._def.errorMap?.(X, J).message ?? J.defaultError;
      if (X.code === "unrecognized_keys") return { message: y.errToObj($).message ?? Q };
      return { message: Q };
    } } : {} });
  }
  strip() {
    return new _Z$({ ...this._def, unknownKeys: "strip" });
  }
  passthrough() {
    return new _Z$({ ...this._def, unknownKeys: "passthrough" });
  }
  extend($) {
    return new _Z$({ ...this._def, shape: () => ({ ...this._def.shape(), ...$ }) });
  }
  merge($) {
    return new _Z$({ unknownKeys: $._def.unknownKeys, catchall: $._def.catchall, shape: () => ({ ...this._def.shape(), ...$._def.shape() }), typeName: P.ZodObject });
  }
  setKey($, X) {
    return this.augment({ [$]: X });
  }
  catchall($) {
    return new _Z$({ ...this._def, catchall: $ });
  }
  pick($) {
    let X = {};
    for (let J of X$.objectKeys($)) if ($[J] && this.shape[J]) X[J] = this.shape[J];
    return new _Z$({ ...this._def, shape: () => X });
  }
  omit($) {
    let X = {};
    for (let J of X$.objectKeys(this.shape)) if (!$[J]) X[J] = this.shape[J];
    return new _Z$({ ...this._def, shape: () => X });
  }
  deepPartial() {
    return W0(this);
  }
  partial($) {
    let X = {};
    for (let J of X$.objectKeys(this.shape)) {
      let Q = this.shape[J];
      if ($ && !$[J]) X[J] = Q;
      else X[J] = Q.optional();
    }
    return new _Z$({ ...this._def, shape: () => X });
  }
  required($) {
    let X = {};
    for (let J of X$.objectKeys(this.shape)) if ($ && !$[J]) X[J] = this.shape[J];
    else {
      let Y = this.shape[J];
      while (Y instanceof M6) Y = Y._def.innerType;
      X[J] = Y;
    }
    return new _Z$({ ...this._def, shape: () => X });
  }
  keyof() {
    return JN(X$.objectKeys(this.shape));
  }
};
Z$.create = ($, X) => {
  return new Z$({ shape: () => $, unknownKeys: "strip", catchall: W4.create(), typeName: P.ZodObject, ...o(X) });
};
Z$.strictCreate = ($, X) => {
  return new Z$({ shape: () => $, unknownKeys: "strict", catchall: W4.create(), typeName: P.ZodObject, ...o(X) });
};
Z$.lazycreate = ($, X) => {
  return new Z$({ shape: $, unknownKeys: "strip", catchall: W4.create(), typeName: P.ZodObject, ...o(X) });
};
var $8 = class extends e {
  _parse($) {
    let { ctx: X } = this._processInputParams($), J = this._def.options;
    function Q(Y) {
      for (let W of Y) if (W.result.status === "valid") return W.result;
      for (let W of Y) if (W.result.status === "dirty") return X.common.issues.push(...W.ctx.common.issues), W.result;
      let z = Y.map((W) => new V6(W.ctx.common.issues));
      return C(X, { code: A.invalid_union, unionErrors: z }), l;
    }
    if (X.common.async) return Promise.all(J.map(async (Y) => {
      let z = { ...X, common: { ...X.common, issues: [] }, parent: null };
      return { result: await Y._parseAsync({ data: X.data, path: X.path, parent: z }), ctx: z };
    })).then(Q);
    else {
      let Y = void 0, z = [];
      for (let G of J) {
        let U = { ...X, common: { ...X.common, issues: [] }, parent: null }, H = G._parseSync({ data: X.data, path: X.path, parent: U });
        if (H.status === "valid") return H;
        else if (H.status === "dirty" && !Y) Y = { result: H, ctx: U };
        if (U.common.issues.length) z.push(U.common.issues);
      }
      if (Y) return X.common.issues.push(...Y.ctx.common.issues), Y.result;
      let W = z.map((G) => new V6(G));
      return C(X, { code: A.invalid_union, unionErrors: W }), l;
    }
  }
  get options() {
    return this._def.options;
  }
};
$8.create = ($, X) => {
  return new $8({ options: $, typeName: P.ZodUnion, ...o(X) });
};
var Q4 = ($) => {
  if ($ instanceof J8) return Q4($.schema);
  else if ($ instanceof p6) return Q4($.innerType());
  else if ($ instanceof Y8) return [$.value];
  else if ($ instanceof q1) return $.options;
  else if ($ instanceof Q8) return X$.objectValues($.enum);
  else if ($ instanceof z8) return Q4($._def.innerType);
  else if ($ instanceof sX) return [void 0];
  else if ($ instanceof eX) return [null];
  else if ($ instanceof M6) return [void 0, ...Q4($.unwrap())];
  else if ($ instanceof v4) return [null, ...Q4($.unwrap())];
  else if ($ instanceof Y5) return Q4($.unwrap());
  else if ($ instanceof G8) return Q4($.unwrap());
  else if ($ instanceof W8) return Q4($._def.innerType);
  else return [];
};
var J5 = class _J5 extends e {
  _parse($) {
    let { ctx: X } = this._processInputParams($);
    if (X.parsedType !== R.object) return C(X, { code: A.invalid_type, expected: R.object, received: X.parsedType }), l;
    let J = this.discriminator, Q = X.data[J], Y = this.optionsMap.get(Q);
    if (!Y) return C(X, { code: A.invalid_union_discriminator, options: Array.from(this.optionsMap.keys()), path: [J] }), l;
    if (X.common.async) return Y._parseAsync({ data: X.data, path: X.path, parent: X });
    else return Y._parseSync({ data: X.data, path: X.path, parent: X });
  }
  get discriminator() {
    return this._def.discriminator;
  }
  get options() {
    return this._def.options;
  }
  get optionsMap() {
    return this._def.optionsMap;
  }
  static create($, X, J) {
    let Q = /* @__PURE__ */ new Map();
    for (let Y of X) {
      let z = Q4(Y.shape[$]);
      if (!z.length) throw Error(`A discriminator value for key \`${$}\` could not be extracted from all schema options`);
      for (let W of z) {
        if (Q.has(W)) throw Error(`Discriminator property ${String($)} has duplicate value ${String(W)}`);
        Q.set(W, Y);
      }
    }
    return new _J5({ typeName: P.ZodDiscriminatedUnion, discriminator: $, options: X, optionsMap: Q, ...o(J) });
  }
};
function X5($, X) {
  let J = Y4($), Q = Y4(X);
  if ($ === X) return { valid: true, data: $ };
  else if (J === R.object && Q === R.object) {
    let Y = X$.objectKeys(X), z = X$.objectKeys($).filter((G) => Y.indexOf(G) !== -1), W = { ...$, ...X };
    for (let G of z) {
      let U = X5($[G], X[G]);
      if (!U.valid) return { valid: false };
      W[G] = U.data;
    }
    return { valid: true, data: W };
  } else if (J === R.array && Q === R.array) {
    if ($.length !== X.length) return { valid: false };
    let Y = [];
    for (let z = 0; z < $.length; z++) {
      let W = $[z], G = X[z], U = X5(W, G);
      if (!U.valid) return { valid: false };
      Y.push(U.data);
    }
    return { valid: true, data: Y };
  } else if (J === R.date && Q === R.date && +$ === +X) return { valid: true, data: $ };
  else return { valid: false };
}
var X8 = class extends e {
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($), Q = (Y, z) => {
      if (sz(Y) || sz(z)) return l;
      let W = X5(Y.value, z.value);
      if (!W.valid) return C(J, { code: A.invalid_intersection_types }), l;
      if (ez(Y) || ez(z)) X.dirty();
      return { status: X.value, value: W.data };
    };
    if (J.common.async) return Promise.all([this._def.left._parseAsync({ data: J.data, path: J.path, parent: J }), this._def.right._parseAsync({ data: J.data, path: J.path, parent: J })]).then(([Y, z]) => Q(Y, z));
    else return Q(this._def.left._parseSync({ data: J.data, path: J.path, parent: J }), this._def.right._parseSync({ data: J.data, path: J.path, parent: J }));
  }
};
X8.create = ($, X, J) => {
  return new X8({ left: $, right: X, typeName: P.ZodIntersection, ...o(J) });
};
var G4 = class _G4 extends e {
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($);
    if (J.parsedType !== R.array) return C(J, { code: A.invalid_type, expected: R.array, received: J.parsedType }), l;
    if (J.data.length < this._def.items.length) return C(J, { code: A.too_small, minimum: this._def.items.length, inclusive: true, exact: false, type: "array" }), l;
    if (!this._def.rest && J.data.length > this._def.items.length) C(J, { code: A.too_big, maximum: this._def.items.length, inclusive: true, exact: false, type: "array" }), X.dirty();
    let Y = [...J.data].map((z, W) => {
      let G = this._def.items[W] || this._def.rest;
      if (!G) return null;
      return G._parse(new v6(J, z, J.path, W));
    }).filter((z) => !!z);
    if (J.common.async) return Promise.all(Y).then((z) => {
      return u$.mergeArray(X, z);
    });
    else return u$.mergeArray(X, Y);
  }
  get items() {
    return this._def.items;
  }
  rest($) {
    return new _G4({ ...this._def, rest: $ });
  }
};
G4.create = ($, X) => {
  if (!Array.isArray($)) throw Error("You must pass an array of schemas to z.tuple([ ... ])");
  return new G4({ items: $, typeName: P.ZodTuple, rest: null, ...o(X) });
};
var gJ = class _gJ extends e {
  get keySchema() {
    return this._def.keyType;
  }
  get valueSchema() {
    return this._def.valueType;
  }
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($);
    if (J.parsedType !== R.object) return C(J, { code: A.invalid_type, expected: R.object, received: J.parsedType }), l;
    let Q = [], Y = this._def.keyType, z = this._def.valueType;
    for (let W in J.data) Q.push({ key: Y._parse(new v6(J, W, J.path, W)), value: z._parse(new v6(J, J.data[W], J.path, W)), alwaysSet: W in J.data });
    if (J.common.async) return u$.mergeObjectAsync(X, Q);
    else return u$.mergeObjectSync(X, Q);
  }
  get element() {
    return this._def.valueType;
  }
  static create($, X, J) {
    if (X instanceof e) return new _gJ({ keyType: $, valueType: X, typeName: P.ZodRecord, ...o(J) });
    return new _gJ({ keyType: z4.create(), valueType: $, typeName: P.ZodRecord, ...o(X) });
  }
};
var hJ = class extends e {
  get keySchema() {
    return this._def.keyType;
  }
  get valueSchema() {
    return this._def.valueType;
  }
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($);
    if (J.parsedType !== R.map) return C(J, { code: A.invalid_type, expected: R.map, received: J.parsedType }), l;
    let Q = this._def.keyType, Y = this._def.valueType, z = [...J.data.entries()].map(([W, G], U) => {
      return { key: Q._parse(new v6(J, W, J.path, [U, "key"])), value: Y._parse(new v6(J, G, J.path, [U, "value"])) };
    });
    if (J.common.async) {
      let W = /* @__PURE__ */ new Map();
      return Promise.resolve().then(async () => {
        for (let G of z) {
          let U = await G.key, H = await G.value;
          if (U.status === "aborted" || H.status === "aborted") return l;
          if (U.status === "dirty" || H.status === "dirty") X.dirty();
          W.set(U.value, H.value);
        }
        return { status: X.value, value: W };
      });
    } else {
      let W = /* @__PURE__ */ new Map();
      for (let G of z) {
        let { key: U, value: H } = G;
        if (U.status === "aborted" || H.status === "aborted") return l;
        if (U.status === "dirty" || H.status === "dirty") X.dirty();
        W.set(U.value, H.value);
      }
      return { status: X.value, value: W };
    }
  }
};
hJ.create = ($, X, J) => {
  return new hJ({ valueType: X, keyType: $, typeName: P.ZodMap, ...o(J) });
};
var H0 = class _H0 extends e {
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($);
    if (J.parsedType !== R.set) return C(J, { code: A.invalid_type, expected: R.set, received: J.parsedType }), l;
    let Q = this._def;
    if (Q.minSize !== null) {
      if (J.data.size < Q.minSize.value) C(J, { code: A.too_small, minimum: Q.minSize.value, type: "set", inclusive: true, exact: false, message: Q.minSize.message }), X.dirty();
    }
    if (Q.maxSize !== null) {
      if (J.data.size > Q.maxSize.value) C(J, { code: A.too_big, maximum: Q.maxSize.value, type: "set", inclusive: true, exact: false, message: Q.maxSize.message }), X.dirty();
    }
    let Y = this._def.valueType;
    function z(G) {
      let U = /* @__PURE__ */ new Set();
      for (let H of G) {
        if (H.status === "aborted") return l;
        if (H.status === "dirty") X.dirty();
        U.add(H.value);
      }
      return { status: X.value, value: U };
    }
    let W = [...J.data.values()].map((G, U) => Y._parse(new v6(J, G, J.path, U)));
    if (J.common.async) return Promise.all(W).then((G) => z(G));
    else return z(W);
  }
  min($, X) {
    return new _H0({ ...this._def, minSize: { value: $, message: y.toString(X) } });
  }
  max($, X) {
    return new _H0({ ...this._def, maxSize: { value: $, message: y.toString(X) } });
  }
  size($, X) {
    return this.min($, X).max($, X);
  }
  nonempty($) {
    return this.min(1, $);
  }
};
H0.create = ($, X) => {
  return new H0({ valueType: $, minSize: null, maxSize: null, typeName: P.ZodSet, ...o(X) });
};
var tX = class _tX extends e {
  constructor() {
    super(...arguments);
    this.validate = this.implement;
  }
  _parse($) {
    let { ctx: X } = this._processInputParams($);
    if (X.parsedType !== R.function) return C(X, { code: A.invalid_type, expected: R.function, received: X.parsedType }), l;
    function J(W, G) {
      return _J({ data: W, path: X.path, errorMaps: [X.common.contextualErrorMap, X.schemaErrorMap, rX(), S4].filter((U) => !!U), issueData: { code: A.invalid_arguments, argumentsError: G } });
    }
    function Q(W, G) {
      return _J({ data: W, path: X.path, errorMaps: [X.common.contextualErrorMap, X.schemaErrorMap, rX(), S4].filter((U) => !!U), issueData: { code: A.invalid_return_type, returnTypeError: G } });
    }
    let Y = { errorMap: X.common.contextualErrorMap }, z = X.data;
    if (this._def.returns instanceof K0) {
      let W = this;
      return n$(async function(...G) {
        let U = new V6([]), H = await W._def.args.parseAsync(G, Y).catch((O) => {
          throw U.addIssue(J(G, O)), U;
        }), K = await Reflect.apply(z, this, H);
        return await W._def.returns._def.type.parseAsync(K, Y).catch((O) => {
          throw U.addIssue(Q(K, O)), U;
        });
      });
    } else {
      let W = this;
      return n$(function(...G) {
        let U = W._def.args.safeParse(G, Y);
        if (!U.success) throw new V6([J(G, U.error)]);
        let H = Reflect.apply(z, this, U.data), K = W._def.returns.safeParse(H, Y);
        if (!K.success) throw new V6([Q(H, K.error)]);
        return K.data;
      });
    }
  }
  parameters() {
    return this._def.args;
  }
  returnType() {
    return this._def.returns;
  }
  args(...$) {
    return new _tX({ ...this._def, args: G4.create($).rest(B1.create()) });
  }
  returns($) {
    return new _tX({ ...this._def, returns: $ });
  }
  implement($) {
    return this.parse($);
  }
  strictImplement($) {
    return this.parse($);
  }
  static create($, X, J) {
    return new _tX({ args: $ ? $ : G4.create([]).rest(B1.create()), returns: X || B1.create(), typeName: P.ZodFunction, ...o(J) });
  }
};
var J8 = class extends e {
  get schema() {
    return this._def.getter();
  }
  _parse($) {
    let { ctx: X } = this._processInputParams($);
    return this._def.getter()._parse({ data: X.data, path: X.path, parent: X });
  }
};
J8.create = ($, X) => {
  return new J8({ getter: $, typeName: P.ZodLazy, ...o(X) });
};
var Y8 = class extends e {
  _parse($) {
    if ($.data !== this._def.value) {
      let X = this._getOrReturnCtx($);
      return C(X, { received: X.data, code: A.invalid_literal, expected: this._def.value }), l;
    }
    return { status: "valid", value: $.data };
  }
  get value() {
    return this._def.value;
  }
};
Y8.create = ($, X) => {
  return new Y8({ value: $, typeName: P.ZodLiteral, ...o(X) });
};
function JN($, X) {
  return new q1({ values: $, typeName: P.ZodEnum, ...o(X) });
}
var q1 = class _q1 extends e {
  _parse($) {
    if (typeof $.data !== "string") {
      let X = this._getOrReturnCtx($), J = this._def.values;
      return C(X, { expected: X$.joinValues(J), received: X.parsedType, code: A.invalid_type }), l;
    }
    if (!this._cache) this._cache = new Set(this._def.values);
    if (!this._cache.has($.data)) {
      let X = this._getOrReturnCtx($), J = this._def.values;
      return C(X, { received: X.data, code: A.invalid_enum_value, options: J }), l;
    }
    return n$($.data);
  }
  get options() {
    return this._def.values;
  }
  get enum() {
    let $ = {};
    for (let X of this._def.values) $[X] = X;
    return $;
  }
  get Values() {
    let $ = {};
    for (let X of this._def.values) $[X] = X;
    return $;
  }
  get Enum() {
    let $ = {};
    for (let X of this._def.values) $[X] = X;
    return $;
  }
  extract($, X = this._def) {
    return _q1.create($, { ...this._def, ...X });
  }
  exclude($, X = this._def) {
    return _q1.create(this.options.filter((J) => !$.includes(J)), { ...this._def, ...X });
  }
};
q1.create = JN;
var Q8 = class extends e {
  _parse($) {
    let X = X$.getValidEnumValues(this._def.values), J = this._getOrReturnCtx($);
    if (J.parsedType !== R.string && J.parsedType !== R.number) {
      let Q = X$.objectValues(X);
      return C(J, { expected: X$.joinValues(Q), received: J.parsedType, code: A.invalid_type }), l;
    }
    if (!this._cache) this._cache = new Set(X$.getValidEnumValues(this._def.values));
    if (!this._cache.has($.data)) {
      let Q = X$.objectValues(X);
      return C(J, { received: J.data, code: A.invalid_enum_value, options: Q }), l;
    }
    return n$($.data);
  }
  get enum() {
    return this._def.values;
  }
};
Q8.create = ($, X) => {
  return new Q8({ values: $, typeName: P.ZodNativeEnum, ...o(X) });
};
var K0 = class extends e {
  unwrap() {
    return this._def.type;
  }
  _parse($) {
    let { ctx: X } = this._processInputParams($);
    if (X.parsedType !== R.promise && X.common.async === false) return C(X, { code: A.invalid_type, expected: R.promise, received: X.parsedType }), l;
    let J = X.parsedType === R.promise ? X.data : Promise.resolve(X.data);
    return n$(J.then((Q) => {
      return this._def.type.parseAsync(Q, { path: X.path, errorMap: X.common.contextualErrorMap });
    }));
  }
};
K0.create = ($, X) => {
  return new K0({ type: $, typeName: P.ZodPromise, ...o(X) });
};
var p6 = class extends e {
  innerType() {
    return this._def.schema;
  }
  sourceType() {
    return this._def.schema._def.typeName === P.ZodEffects ? this._def.schema.sourceType() : this._def.schema;
  }
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($), Q = this._def.effect || null, Y = { addIssue: (z) => {
      if (C(J, z), z.fatal) X.abort();
      else X.dirty();
    }, get path() {
      return J.path;
    } };
    if (Y.addIssue = Y.addIssue.bind(Y), Q.type === "preprocess") {
      let z = Q.transform(J.data, Y);
      if (J.common.async) return Promise.resolve(z).then(async (W) => {
        if (X.value === "aborted") return l;
        let G = await this._def.schema._parseAsync({ data: W, path: J.path, parent: J });
        if (G.status === "aborted") return l;
        if (G.status === "dirty") return z0(G.value);
        if (X.value === "dirty") return z0(G.value);
        return G;
      });
      else {
        if (X.value === "aborted") return l;
        let W = this._def.schema._parseSync({ data: z, path: J.path, parent: J });
        if (W.status === "aborted") return l;
        if (W.status === "dirty") return z0(W.value);
        if (X.value === "dirty") return z0(W.value);
        return W;
      }
    }
    if (Q.type === "refinement") {
      let z = (W) => {
        let G = Q.refinement(W, Y);
        if (J.common.async) return Promise.resolve(G);
        if (G instanceof Promise) throw Error("Async refinement encountered during synchronous parse operation. Use .parseAsync instead.");
        return W;
      };
      if (J.common.async === false) {
        let W = this._def.schema._parseSync({ data: J.data, path: J.path, parent: J });
        if (W.status === "aborted") return l;
        if (W.status === "dirty") X.dirty();
        return z(W.value), { status: X.value, value: W.value };
      } else return this._def.schema._parseAsync({ data: J.data, path: J.path, parent: J }).then((W) => {
        if (W.status === "aborted") return l;
        if (W.status === "dirty") X.dirty();
        return z(W.value).then(() => {
          return { status: X.value, value: W.value };
        });
      });
    }
    if (Q.type === "transform") if (J.common.async === false) {
      let z = this._def.schema._parseSync({ data: J.data, path: J.path, parent: J });
      if (!w1(z)) return l;
      let W = Q.transform(z.value, Y);
      if (W instanceof Promise) throw Error("Asynchronous transform encountered during synchronous parse operation. Use .parseAsync instead.");
      return { status: X.value, value: W };
    } else return this._def.schema._parseAsync({ data: J.data, path: J.path, parent: J }).then((z) => {
      if (!w1(z)) return l;
      return Promise.resolve(Q.transform(z.value, Y)).then((W) => ({ status: X.value, value: W }));
    });
    X$.assertNever(Q);
  }
};
p6.create = ($, X, J) => {
  return new p6({ schema: $, typeName: P.ZodEffects, effect: X, ...o(J) });
};
p6.createWithPreprocess = ($, X, J) => {
  return new p6({ schema: X, effect: { type: "preprocess", transform: $ }, typeName: P.ZodEffects, ...o(J) });
};
var M6 = class extends e {
  _parse($) {
    if (this._getType($) === R.undefined) return n$(void 0);
    return this._def.innerType._parse($);
  }
  unwrap() {
    return this._def.innerType;
  }
};
M6.create = ($, X) => {
  return new M6({ innerType: $, typeName: P.ZodOptional, ...o(X) });
};
var v4 = class extends e {
  _parse($) {
    if (this._getType($) === R.null) return n$(null);
    return this._def.innerType._parse($);
  }
  unwrap() {
    return this._def.innerType;
  }
};
v4.create = ($, X) => {
  return new v4({ innerType: $, typeName: P.ZodNullable, ...o(X) });
};
var z8 = class extends e {
  _parse($) {
    let { ctx: X } = this._processInputParams($), J = X.data;
    if (X.parsedType === R.undefined) J = this._def.defaultValue();
    return this._def.innerType._parse({ data: J, path: X.path, parent: X });
  }
  removeDefault() {
    return this._def.innerType;
  }
};
z8.create = ($, X) => {
  return new z8({ innerType: $, typeName: P.ZodDefault, defaultValue: typeof X.default === "function" ? X.default : () => X.default, ...o(X) });
};
var W8 = class extends e {
  _parse($) {
    let { ctx: X } = this._processInputParams($), J = { ...X, common: { ...X.common, issues: [] } }, Q = this._def.innerType._parse({ data: J.data, path: J.path, parent: { ...J } });
    if (oX(Q)) return Q.then((Y) => {
      return { status: "valid", value: Y.status === "valid" ? Y.value : this._def.catchValue({ get error() {
        return new V6(J.common.issues);
      }, input: J.data }) };
    });
    else return { status: "valid", value: Q.status === "valid" ? Q.value : this._def.catchValue({ get error() {
      return new V6(J.common.issues);
    }, input: J.data }) };
  }
  removeCatch() {
    return this._def.innerType;
  }
};
W8.create = ($, X) => {
  return new W8({ innerType: $, typeName: P.ZodCatch, catchValue: typeof X.catch === "function" ? X.catch : () => X.catch, ...o(X) });
};
var uJ = class extends e {
  _parse($) {
    if (this._getType($) !== R.nan) {
      let J = this._getOrReturnCtx($);
      return C(J, { code: A.invalid_type, expected: R.nan, received: J.parsedType }), l;
    }
    return { status: "valid", value: $.data };
  }
};
uJ.create = ($) => {
  return new uJ({ typeName: P.ZodNaN, ...o($) });
};
var dl = Symbol("zod_brand");
var Y5 = class extends e {
  _parse($) {
    let { ctx: X } = this._processInputParams($), J = X.data;
    return this._def.type._parse({ data: J, path: X.path, parent: X });
  }
  unwrap() {
    return this._def.type;
  }
};
var mJ = class _mJ extends e {
  _parse($) {
    let { status: X, ctx: J } = this._processInputParams($);
    if (J.common.async) return (async () => {
      let Y = await this._def.in._parseAsync({ data: J.data, path: J.path, parent: J });
      if (Y.status === "aborted") return l;
      if (Y.status === "dirty") return X.dirty(), z0(Y.value);
      else return this._def.out._parseAsync({ data: Y.value, path: J.path, parent: J });
    })();
    else {
      let Q = this._def.in._parseSync({ data: J.data, path: J.path, parent: J });
      if (Q.status === "aborted") return l;
      if (Q.status === "dirty") return X.dirty(), { status: "dirty", value: Q.value };
      else return this._def.out._parseSync({ data: Q.value, path: J.path, parent: J });
    }
  }
  static create($, X) {
    return new _mJ({ in: $, out: X, typeName: P.ZodPipeline });
  }
};
var G8 = class extends e {
  _parse($) {
    let X = this._def.innerType._parse($), J = (Q) => {
      if (w1(Q)) Q.value = Object.freeze(Q.value);
      return Q;
    };
    return oX(X) ? X.then((Q) => J(Q)) : J(X);
  }
  unwrap() {
    return this._def.innerType;
  }
};
G8.create = ($, X) => {
  return new G8({ innerType: $, typeName: P.ZodReadonly, ...o(X) });
};
var rl = { object: Z$.lazycreate };
var P;
(function($) {
  $.ZodString = "ZodString", $.ZodNumber = "ZodNumber", $.ZodNaN = "ZodNaN", $.ZodBigInt = "ZodBigInt", $.ZodBoolean = "ZodBoolean", $.ZodDate = "ZodDate", $.ZodSymbol = "ZodSymbol", $.ZodUndefined = "ZodUndefined", $.ZodNull = "ZodNull", $.ZodAny = "ZodAny", $.ZodUnknown = "ZodUnknown", $.ZodNever = "ZodNever", $.ZodVoid = "ZodVoid", $.ZodArray = "ZodArray", $.ZodObject = "ZodObject", $.ZodUnion = "ZodUnion", $.ZodDiscriminatedUnion = "ZodDiscriminatedUnion", $.ZodIntersection = "ZodIntersection", $.ZodTuple = "ZodTuple", $.ZodRecord = "ZodRecord", $.ZodMap = "ZodMap", $.ZodSet = "ZodSet", $.ZodFunction = "ZodFunction", $.ZodLazy = "ZodLazy", $.ZodLiteral = "ZodLiteral", $.ZodEnum = "ZodEnum", $.ZodEffects = "ZodEffects", $.ZodNativeEnum = "ZodNativeEnum", $.ZodOptional = "ZodOptional", $.ZodNullable = "ZodNullable", $.ZodDefault = "ZodDefault", $.ZodCatch = "ZodCatch", $.ZodPromise = "ZodPromise", $.ZodBranded = "ZodBranded", $.ZodPipeline = "ZodPipeline", $.ZodReadonly = "ZodReadonly";
})(P || (P = {}));
var ol = z4.create;
var tl = G0.create;
var al = uJ.create;
var sl = U0.create;
var el = xJ.create;
var $c = aX.create;
var Xc = TJ.create;
var Jc = sX.create;
var Yc = eX.create;
var Qc = yJ.create;
var zc = B1.create;
var Wc = W4.create;
var Gc = fJ.create;
var Uc = c6.create;
var YN = Z$.create;
var Hc = Z$.strictCreate;
var Kc = $8.create;
var Nc = J5.create;
var Vc = X8.create;
var Oc = G4.create;
var wc = gJ.create;
var Bc = hJ.create;
var qc = H0.create;
var Dc = tX.create;
var Lc = J8.create;
var jc = Y8.create;
var Fc = q1.create;
var Mc = Q8.create;
var Ic = K0.create;
var Ac = p6.create;
var bc = M6.create;
var Pc = v4.create;
var Zc = p6.createWithPreprocess;
var Ec = mJ.create;
var C6 = {};
$1(C6, { version: () => GW, util: () => E, treeifyError: () => iJ, toJSONSchema: () => Z0, toDotPath: () => WN, safeParseAsync: () => _4, safeParse: () => k4, registry: () => A8, regexes: () => x4, prettifyError: () => nJ, parseAsync: () => F1, parse: () => j1, locales: () => M0, isValidJWT: () => bN, isValidBase64URL: () => AN, isValidBase64: () => OW, globalRegistry: () => X6, globalConfig: () => U8, function: () => Z7, formatError: () => B0, flattenError: () => w0, config: () => E$, clone: () => m$, _xid: () => T8, _void: () => L7, _uuidv7: () => R8, _uuidv6: () => E8, _uuidv4: () => Z8, _uuid: () => P8, _url: () => S8, _uppercase: () => r8, _unknown: () => A1, _union: () => Y2, _undefined: () => w7, _ulid: () => x8, _uint64: () => V7, _uint32: () => U7, _tuple: () => VG, _trim: () => $9, _transform: () => V2, _toUpperCase: () => J9, _toLowerCase: () => X9, _templateLiteral: () => M2, _symbol: () => O7, _success: () => D2, _stringbool: () => b7, _stringFormat: () => P7, _string: () => X7, _startsWith: () => t8, _size: () => i8, _set: () => U2, _safeParseAsync: () => tJ, _safeParse: () => oJ, _regex: () => n8, _refine: () => A7, _record: () => W2, _readonly: () => F2, _property: () => NG, _promise: () => A2, _positive: () => GG, _pipe: () => j2, _parseAsync: () => rJ, _parse: () => dJ, _overwrite: () => V4, _optional: () => O2, _number: () => Y7, _nullable: () => w2, _null: () => B7, _normalize: () => e8, _nonpositive: () => HG, _nonoptional: () => q2, _nonnegative: () => KG, _never: () => D7, _negative: () => UG, _nativeEnum: () => K2, _nanoid: () => C8, _nan: () => F7, _multipleOf: () => b1, _minSize: () => P1, _minLength: () => f4, _min: () => J6, _mime: () => s8, _maxSize: () => A0, _maxLength: () => b0, _max: () => I6, _map: () => G2, _lte: () => I6, _lt: () => K4, _lowercase: () => d8, _literal: () => N2, _length: () => P0, _lazy: () => I2, _ksuid: () => y8, _jwt: () => p8, _isoTime: () => XG, _isoDuration: () => JG, _isoDateTime: () => eW, _isoDate: () => $G, _ipv6: () => g8, _ipv4: () => f8, _intersection: () => z2, _int64: () => N7, _int32: () => G7, _int: () => Q7, _includes: () => o8, _guid: () => I0, _gte: () => J6, _gt: () => N4, _float64: () => W7, _float32: () => z7, _file: () => M7, _enum: () => H2, _endsWith: () => a8, _emoji: () => v8, _email: () => b8, _e164: () => c8, _discriminatedUnion: () => Q2, _default: () => B2, _date: () => j7, _custom: () => I7, _cuid2: () => _8, _cuid: () => k8, _coercedString: () => sW, _coercedNumber: () => YG, _coercedDate: () => WG, _coercedBoolean: () => QG, _coercedBigint: () => zG, _cidrv6: () => u8, _cidrv4: () => h8, _catch: () => L2, _boolean: () => H7, _bigint: () => K7, _base64url: () => l8, _base64: () => m8, _array: () => Y9, _any: () => q7, TimePrecision: () => J7, NEVER: () => lJ, JSONSchemaGenerator: () => E7, JSONSchema: () => RN, Doc: () => $Y, $output: () => eY, $input: () => $7, $constructor: () => q, $brand: () => cJ, $ZodXID: () => VY, $ZodVoid: () => vY, $ZodUnknown: () => I1, $ZodUnion: () => F8, $ZodUndefined: () => ZY, $ZodUUID: () => QY, $ZodURL: () => WY, $ZodULID: () => NY, $ZodType: () => i, $ZodTuple: () => y4, $ZodTransform: () => j0, $ZodTemplateLiteral: () => oY, $ZodSymbol: () => PY, $ZodSuccess: () => iY, $ZodStringFormat: () => H$, $ZodString: () => T4, $ZodSet: () => yY, $ZodRegistry: () => I8, $ZodRecord: () => xY, $ZodRealError: () => O0, $ZodReadonly: () => rY, $ZodPromise: () => tY, $ZodPrefault: () => cY, $ZodPipe: () => F0, $ZodOptional: () => uY, $ZodObject: () => j8, $ZodNumberFormat: () => AY, $ZodNumber: () => D8, $ZodNullable: () => mY, $ZodNull: () => EY, $ZodNonOptional: () => pY, $ZodNever: () => SY, $ZodNanoID: () => UY, $ZodNaN: () => dY, $ZodMap: () => TY, $ZodLiteral: () => gY, $ZodLazy: () => aY, $ZodKSUID: () => OY, $ZodJWT: () => MY, $ZodIntersection: () => _Y, $ZodISOTime: () => NW, $ZodISODuration: () => VW, $ZodISODateTime: () => HW, $ZodISODate: () => KW, $ZodIPv6: () => BY, $ZodIPv4: () => wY, $ZodGUID: () => YY, $ZodFunction: () => OG, $ZodFile: () => hY, $ZodError: () => q8, $ZodEnum: () => fY, $ZodEmoji: () => GY, $ZodEmail: () => zY, $ZodE164: () => FY, $ZodDiscriminatedUnion: () => kY, $ZodDefault: () => lY, $ZodDate: () => CY, $ZodCustomStringFormat: () => IY, $ZodCustom: () => sY, $ZodCheckUpperCase: () => $W, $ZodCheckStringFormat: () => q0, $ZodCheckStartsWith: () => JW, $ZodCheckSizeEquals: () => r5, $ZodCheckRegex: () => s5, $ZodCheckProperty: () => QW, $ZodCheckOverwrite: () => WW, $ZodCheckNumberFormat: () => p5, $ZodCheckMultipleOf: () => c5, $ZodCheckMinSize: () => d5, $ZodCheckMinLength: () => t5, $ZodCheckMimeType: () => zW, $ZodCheckMaxSize: () => n5, $ZodCheckMaxLength: () => o5, $ZodCheckLowerCase: () => e5, $ZodCheckLessThan: () => sJ, $ZodCheckLengthEquals: () => a5, $ZodCheckIncludes: () => XW, $ZodCheckGreaterThan: () => eJ, $ZodCheckEndsWith: () => YW, $ZodCheckBigIntFormat: () => i5, $ZodCheck: () => M$, $ZodCatch: () => nY, $ZodCUID2: () => KY, $ZodCUID: () => HY, $ZodCIDRv6: () => DY, $ZodCIDRv4: () => qY, $ZodBoolean: () => D0, $ZodBigIntFormat: () => bY, $ZodBigInt: () => L8, $ZodBase64URL: () => jY, $ZodBase64: () => LY, $ZodAsyncError: () => U4, $ZodArray: () => L0, $ZodAny: () => RY });
var lJ = Object.freeze({ status: "aborted" });
function q($, X, J) {
  function Q(G, U) {
    var H;
    Object.defineProperty(G, "_zod", { value: G._zod ?? {}, enumerable: false }), (H = G._zod).traits ?? (H.traits = /* @__PURE__ */ new Set()), G._zod.traits.add($), X(G, U);
    for (let K in W.prototype) if (!(K in G)) Object.defineProperty(G, K, { value: W.prototype[K].bind(G) });
    G._zod.constr = W, G._zod.def = U;
  }
  let Y = J?.Parent ?? Object;
  class z extends Y {
  }
  Object.defineProperty(z, "name", { value: $ });
  function W(G) {
    var U;
    let H = J?.Parent ? new z() : this;
    Q(H, G), (U = H._zod).deferred ?? (U.deferred = []);
    for (let K of H._zod.deferred) K();
    return H;
  }
  return Object.defineProperty(W, "init", { value: Q }), Object.defineProperty(W, Symbol.hasInstance, { value: (G) => {
    if (J?.Parent && G instanceof J.Parent) return true;
    return G?._zod?.traits?.has($);
  } }), Object.defineProperty(W, "name", { value: $ }), W;
}
var cJ = Symbol("zod_brand");
var U4 = class extends Error {
  constructor() {
    super("Encountered Promise during synchronous parse. Use .parseAsync() instead.");
  }
};
var U8 = {};
function E$($) {
  if ($) Object.assign(U8, $);
  return U8;
}
var E = {};
$1(E, { unwrapMessage: () => H8, stringifyPrimitive: () => S, required: () => JA, randomString: () => dI, propertyKeyTypes: () => O8, promiseAllObject: () => nI, primitiveTypes: () => H5, prefixIssues: () => $6, pick: () => aI, partial: () => XA, optionalKeys: () => K5, omit: () => sI, numKeys: () => rI, nullish: () => C4, normalizeParams: () => Z, merge: () => $A, jsonStringifyReplacer: () => z5, joinValues: () => M, issue: () => O5, isPlainObject: () => V0, isObject: () => N0, getSizableOrigin: () => w8, getParsedType: () => oI, getLengthableOrigin: () => B8, getEnumValues: () => K8, getElementAtPath: () => iI, floatSafeRemainder: () => W5, finalizeIssue: () => O6, extend: () => eI, escapeRegex: () => H4, esc: () => D1, defineLazy: () => W$, createTransparentProxy: () => tI, clone: () => m$, cleanRegex: () => V8, cleanEnum: () => YA, captureStackTrace: () => pJ, cached: () => N8, assignProp: () => G5, assertNotEqual: () => mI, assertNever: () => cI, assertIs: () => lI, assertEqual: () => uI, assert: () => pI, allowsEval: () => U5, aborted: () => L1, NUMBER_FORMAT_RANGES: () => N5, Class: () => QN, BIGINT_FORMAT_RANGES: () => V5 });
function uI($) {
  return $;
}
function mI($) {
  return $;
}
function lI($) {
}
function cI($) {
  throw Error();
}
function pI($) {
}
function K8($) {
  let X = Object.values($).filter((Q) => typeof Q === "number");
  return Object.entries($).filter(([Q, Y]) => X.indexOf(+Q) === -1).map(([Q, Y]) => Y);
}
function M($, X = "|") {
  return $.map((J) => S(J)).join(X);
}
function z5($, X) {
  if (typeof X === "bigint") return X.toString();
  return X;
}
function N8($) {
  return { get value() {
    {
      let J = $();
      return Object.defineProperty(this, "value", { value: J }), J;
    }
    throw Error("cached value already set");
  } };
}
function C4($) {
  return $ === null || $ === void 0;
}
function V8($) {
  let X = $.startsWith("^") ? 1 : 0, J = $.endsWith("$") ? $.length - 1 : $.length;
  return $.slice(X, J);
}
function W5($, X) {
  let J = ($.toString().split(".")[1] || "").length, Q = (X.toString().split(".")[1] || "").length, Y = J > Q ? J : Q, z = Number.parseInt($.toFixed(Y).replace(".", "")), W = Number.parseInt(X.toFixed(Y).replace(".", ""));
  return z % W / 10 ** Y;
}
function W$($, X, J) {
  Object.defineProperty($, X, { get() {
    {
      let Y = J();
      return $[X] = Y, Y;
    }
    throw Error("cached value already set");
  }, set(Y) {
    Object.defineProperty($, X, { value: Y });
  }, configurable: true });
}
function G5($, X, J) {
  Object.defineProperty($, X, { value: J, writable: true, enumerable: true, configurable: true });
}
function iI($, X) {
  if (!X) return $;
  return X.reduce((J, Q) => J?.[Q], $);
}
function nI($) {
  let X = Object.keys($), J = X.map((Q) => $[Q]);
  return Promise.all(J).then((Q) => {
    let Y = {};
    for (let z = 0; z < X.length; z++) Y[X[z]] = Q[z];
    return Y;
  });
}
function dI($ = 10) {
  let J = "";
  for (let Q = 0; Q < $; Q++) J += "abcdefghijklmnopqrstuvwxyz"[Math.floor(Math.random() * 26)];
  return J;
}
function D1($) {
  return JSON.stringify($);
}
var pJ = Error.captureStackTrace ? Error.captureStackTrace : (...$) => {
};
function N0($) {
  return typeof $ === "object" && $ !== null && !Array.isArray($);
}
var U5 = N8(() => {
  if (typeof navigator < "u" && navigator?.userAgent?.includes("Cloudflare")) return false;
  try {
    return new Function(""), true;
  } catch ($) {
    return false;
  }
});
function V0($) {
  if (N0($) === false) return false;
  let X = $.constructor;
  if (X === void 0) return true;
  let J = X.prototype;
  if (N0(J) === false) return false;
  if (Object.prototype.hasOwnProperty.call(J, "isPrototypeOf") === false) return false;
  return true;
}
function rI($) {
  let X = 0;
  for (let J in $) if (Object.prototype.hasOwnProperty.call($, J)) X++;
  return X;
}
var oI = ($) => {
  let X = typeof $;
  switch (X) {
    case "undefined":
      return "undefined";
    case "string":
      return "string";
    case "number":
      return Number.isNaN($) ? "nan" : "number";
    case "boolean":
      return "boolean";
    case "function":
      return "function";
    case "bigint":
      return "bigint";
    case "symbol":
      return "symbol";
    case "object":
      if (Array.isArray($)) return "array";
      if ($ === null) return "null";
      if ($.then && typeof $.then === "function" && $.catch && typeof $.catch === "function") return "promise";
      if (typeof Map < "u" && $ instanceof Map) return "map";
      if (typeof Set < "u" && $ instanceof Set) return "set";
      if (typeof Date < "u" && $ instanceof Date) return "date";
      if (typeof File < "u" && $ instanceof File) return "file";
      return "object";
    default:
      throw Error(`Unknown data type: ${X}`);
  }
};
var O8 = /* @__PURE__ */ new Set(["string", "number", "symbol"]);
var H5 = /* @__PURE__ */ new Set(["string", "number", "bigint", "boolean", "symbol", "undefined"]);
function H4($) {
  return $.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
function m$($, X, J) {
  let Q = new $._zod.constr(X ?? $._zod.def);
  if (!X || J?.parent) Q._zod.parent = $;
  return Q;
}
function Z($) {
  let X = $;
  if (!X) return {};
  if (typeof X === "string") return { error: () => X };
  if (X?.message !== void 0) {
    if (X?.error !== void 0) throw Error("Cannot specify both `message` and `error` params");
    X.error = X.message;
  }
  if (delete X.message, typeof X.error === "string") return { ...X, error: () => X.error };
  return X;
}
function tI($) {
  let X;
  return new Proxy({}, { get(J, Q, Y) {
    return X ?? (X = $()), Reflect.get(X, Q, Y);
  }, set(J, Q, Y, z) {
    return X ?? (X = $()), Reflect.set(X, Q, Y, z);
  }, has(J, Q) {
    return X ?? (X = $()), Reflect.has(X, Q);
  }, deleteProperty(J, Q) {
    return X ?? (X = $()), Reflect.deleteProperty(X, Q);
  }, ownKeys(J) {
    return X ?? (X = $()), Reflect.ownKeys(X);
  }, getOwnPropertyDescriptor(J, Q) {
    return X ?? (X = $()), Reflect.getOwnPropertyDescriptor(X, Q);
  }, defineProperty(J, Q, Y) {
    return X ?? (X = $()), Reflect.defineProperty(X, Q, Y);
  } });
}
function S($) {
  if (typeof $ === "bigint") return $.toString() + "n";
  if (typeof $ === "string") return `"${$}"`;
  return `${$}`;
}
function K5($) {
  return Object.keys($).filter((X) => {
    return $[X]._zod.optin === "optional" && $[X]._zod.optout === "optional";
  });
}
var N5 = { safeint: [Number.MIN_SAFE_INTEGER, Number.MAX_SAFE_INTEGER], int32: [-2147483648, 2147483647], uint32: [0, 4294967295], float32: [-34028234663852886e22, 34028234663852886e22], float64: [-Number.MAX_VALUE, Number.MAX_VALUE] };
var V5 = { int64: [BigInt("-9223372036854775808"), BigInt("9223372036854775807")], uint64: [BigInt(0), BigInt("18446744073709551615")] };
function aI($, X) {
  let J = {}, Q = $._zod.def;
  for (let Y in X) {
    if (!(Y in Q.shape)) throw Error(`Unrecognized key: "${Y}"`);
    if (!X[Y]) continue;
    J[Y] = Q.shape[Y];
  }
  return m$($, { ...$._zod.def, shape: J, checks: [] });
}
function sI($, X) {
  let J = { ...$._zod.def.shape }, Q = $._zod.def;
  for (let Y in X) {
    if (!(Y in Q.shape)) throw Error(`Unrecognized key: "${Y}"`);
    if (!X[Y]) continue;
    delete J[Y];
  }
  return m$($, { ...$._zod.def, shape: J, checks: [] });
}
function eI($, X) {
  if (!V0(X)) throw Error("Invalid input to extend: expected a plain object");
  let J = { ...$._zod.def, get shape() {
    let Q = { ...$._zod.def.shape, ...X };
    return G5(this, "shape", Q), Q;
  }, checks: [] };
  return m$($, J);
}
function $A($, X) {
  return m$($, { ...$._zod.def, get shape() {
    let J = { ...$._zod.def.shape, ...X._zod.def.shape };
    return G5(this, "shape", J), J;
  }, catchall: X._zod.def.catchall, checks: [] });
}
function XA($, X, J) {
  let Q = X._zod.def.shape, Y = { ...Q };
  if (J) for (let z in J) {
    if (!(z in Q)) throw Error(`Unrecognized key: "${z}"`);
    if (!J[z]) continue;
    Y[z] = $ ? new $({ type: "optional", innerType: Q[z] }) : Q[z];
  }
  else for (let z in Q) Y[z] = $ ? new $({ type: "optional", innerType: Q[z] }) : Q[z];
  return m$(X, { ...X._zod.def, shape: Y, checks: [] });
}
function JA($, X, J) {
  let Q = X._zod.def.shape, Y = { ...Q };
  if (J) for (let z in J) {
    if (!(z in Y)) throw Error(`Unrecognized key: "${z}"`);
    if (!J[z]) continue;
    Y[z] = new $({ type: "nonoptional", innerType: Q[z] });
  }
  else for (let z in Q) Y[z] = new $({ type: "nonoptional", innerType: Q[z] });
  return m$(X, { ...X._zod.def, shape: Y, checks: [] });
}
function L1($, X = 0) {
  for (let J = X; J < $.issues.length; J++) if ($.issues[J]?.continue !== true) return true;
  return false;
}
function $6($, X) {
  return X.map((J) => {
    var Q;
    return (Q = J).path ?? (Q.path = []), J.path.unshift($), J;
  });
}
function H8($) {
  return typeof $ === "string" ? $ : $?.message;
}
function O6($, X, J) {
  let Q = { ...$, path: $.path ?? [] };
  if (!$.message) {
    let Y = H8($.inst?._zod.def?.error?.($)) ?? H8(X?.error?.($)) ?? H8(J.customError?.($)) ?? H8(J.localeError?.($)) ?? "Invalid input";
    Q.message = Y;
  }
  if (delete Q.inst, delete Q.continue, !X?.reportInput) delete Q.input;
  return Q;
}
function w8($) {
  if ($ instanceof Set) return "set";
  if ($ instanceof Map) return "map";
  if ($ instanceof File) return "file";
  return "unknown";
}
function B8($) {
  if (Array.isArray($)) return "array";
  if (typeof $ === "string") return "string";
  return "unknown";
}
function O5(...$) {
  let [X, J, Q] = $;
  if (typeof X === "string") return { message: X, code: "custom", input: J, inst: Q };
  return { ...X };
}
function YA($) {
  return Object.entries($).filter(([X, J]) => {
    return Number.isNaN(Number.parseInt(X, 10));
  }).map((X) => X[1]);
}
var QN = class {
  constructor(...$) {
  }
};
var zN = ($, X) => {
  $.name = "$ZodError", Object.defineProperty($, "_zod", { value: $._zod, enumerable: false }), Object.defineProperty($, "issues", { value: X, enumerable: false }), Object.defineProperty($, "message", { get() {
    return JSON.stringify(X, z5, 2);
  }, enumerable: true });
};
var q8 = q("$ZodError", zN);
var O0 = q("$ZodError", zN, { Parent: Error });
function w0($, X = (J) => J.message) {
  let J = {}, Q = [];
  for (let Y of $.issues) if (Y.path.length > 0) J[Y.path[0]] = J[Y.path[0]] || [], J[Y.path[0]].push(X(Y));
  else Q.push(X(Y));
  return { formErrors: Q, fieldErrors: J };
}
function B0($, X) {
  let J = X || function(z) {
    return z.message;
  }, Q = { _errors: [] }, Y = (z) => {
    for (let W of z.issues) if (W.code === "invalid_union" && W.errors.length) W.errors.map((G) => Y({ issues: G }));
    else if (W.code === "invalid_key") Y({ issues: W.issues });
    else if (W.code === "invalid_element") Y({ issues: W.issues });
    else if (W.path.length === 0) Q._errors.push(J(W));
    else {
      let G = Q, U = 0;
      while (U < W.path.length) {
        let H = W.path[U];
        if (U !== W.path.length - 1) G[H] = G[H] || { _errors: [] };
        else G[H] = G[H] || { _errors: [] }, G[H]._errors.push(J(W));
        G = G[H], U++;
      }
    }
  };
  return Y($), Q;
}
function iJ($, X) {
  let J = X || function(z) {
    return z.message;
  }, Q = { errors: [] }, Y = (z, W = []) => {
    var G, U;
    for (let H of z.issues) if (H.code === "invalid_union" && H.errors.length) H.errors.map((K) => Y({ issues: K }, H.path));
    else if (H.code === "invalid_key") Y({ issues: H.issues }, H.path);
    else if (H.code === "invalid_element") Y({ issues: H.issues }, H.path);
    else {
      let K = [...W, ...H.path];
      if (K.length === 0) {
        Q.errors.push(J(H));
        continue;
      }
      let V = Q, O = 0;
      while (O < K.length) {
        let N = K[O], w = O === K.length - 1;
        if (typeof N === "string") V.properties ?? (V.properties = {}), (G = V.properties)[N] ?? (G[N] = { errors: [] }), V = V.properties[N];
        else V.items ?? (V.items = []), (U = V.items)[N] ?? (U[N] = { errors: [] }), V = V.items[N];
        if (w) V.errors.push(J(H));
        O++;
      }
    }
  };
  return Y($), Q;
}
function WN($) {
  let X = [];
  for (let J of $) if (typeof J === "number") X.push(`[${J}]`);
  else if (typeof J === "symbol") X.push(`[${JSON.stringify(String(J))}]`);
  else if (/[^\w$]/.test(J)) X.push(`[${JSON.stringify(J)}]`);
  else {
    if (X.length) X.push(".");
    X.push(J);
  }
  return X.join("");
}
function nJ($) {
  let X = [], J = [...$.issues].sort((Q, Y) => Q.path.length - Y.path.length);
  for (let Q of J) if (X.push(`\u2716 ${Q.message}`), Q.path?.length) X.push(`  \u2192 at ${WN(Q.path)}`);
  return X.join(`
`);
}
var dJ = ($) => (X, J, Q, Y) => {
  let z = Q ? Object.assign(Q, { async: false }) : { async: false }, W = X._zod.run({ value: J, issues: [] }, z);
  if (W instanceof Promise) throw new U4();
  if (W.issues.length) {
    let G = new (Y?.Err ?? $)(W.issues.map((U) => O6(U, z, E$())));
    throw pJ(G, Y?.callee), G;
  }
  return W.value;
};
var j1 = dJ(O0);
var rJ = ($) => async (X, J, Q, Y) => {
  let z = Q ? Object.assign(Q, { async: true }) : { async: true }, W = X._zod.run({ value: J, issues: [] }, z);
  if (W instanceof Promise) W = await W;
  if (W.issues.length) {
    let G = new (Y?.Err ?? $)(W.issues.map((U) => O6(U, z, E$())));
    throw pJ(G, Y?.callee), G;
  }
  return W.value;
};
var F1 = rJ(O0);
var oJ = ($) => (X, J, Q) => {
  let Y = Q ? { ...Q, async: false } : { async: false }, z = X._zod.run({ value: J, issues: [] }, Y);
  if (z instanceof Promise) throw new U4();
  return z.issues.length ? { success: false, error: new ($ ?? q8)(z.issues.map((W) => O6(W, Y, E$()))) } : { success: true, data: z.value };
};
var k4 = oJ(O0);
var tJ = ($) => async (X, J, Q) => {
  let Y = Q ? Object.assign(Q, { async: true }) : { async: true }, z = X._zod.run({ value: J, issues: [] }, Y);
  if (z instanceof Promise) z = await z;
  return z.issues.length ? { success: false, error: new $(z.issues.map((W) => O6(W, Y, E$()))) } : { success: true, data: z.value };
};
var _4 = tJ(O0);
var x4 = {};
$1(x4, { xid: () => D5, uuid7: () => UA, uuid6: () => GA, uuid4: () => WA, uuid: () => M1, uppercase: () => l5, unicodeEmail: () => NA, undefined: () => u5, ulid: () => q5, time: () => k5, string: () => x5, rfc5322Email: () => KA, number: () => f5, null: () => h5, nanoid: () => j5, lowercase: () => m5, ksuid: () => L5, ipv6: () => P5, ipv4: () => b5, integer: () => y5, html5Email: () => HA, hostname: () => S5, guid: () => M5, extendedDuration: () => zA, emoji: () => A5, email: () => I5, e164: () => v5, duration: () => F5, domain: () => wA, datetime: () => _5, date: () => C5, cuid2: () => B5, cuid: () => w5, cidrv6: () => E5, cidrv4: () => Z5, browserEmail: () => VA, boolean: () => g5, bigint: () => T5, base64url: () => aJ, base64: () => R5, _emoji: () => OA });
var w5 = /^[cC][^\s-]{8,}$/;
var B5 = /^[0-9a-z]+$/;
var q5 = /^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{26}$/;
var D5 = /^[0-9a-vA-V]{20}$/;
var L5 = /^[A-Za-z0-9]{27}$/;
var j5 = /^[a-zA-Z0-9_-]{21}$/;
var F5 = /^P(?:(\d+W)|(?!.*W)(?=\d|T\d)(\d+Y)?(\d+M)?(\d+D)?(T(?=\d)(\d+H)?(\d+M)?(\d+([.,]\d+)?S)?)?)$/;
var zA = /^[-+]?P(?!$)(?:(?:[-+]?\d+Y)|(?:[-+]?\d+[.,]\d+Y$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:(?:[-+]?\d+W)|(?:[-+]?\d+[.,]\d+W$))?(?:(?:[-+]?\d+D)|(?:[-+]?\d+[.,]\d+D$))?(?:T(?=[\d+-])(?:(?:[-+]?\d+H)|(?:[-+]?\d+[.,]\d+H$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:[-+]?\d+(?:[.,]\d+)?S)?)??$/;
var M5 = /^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$/;
var M1 = ($) => {
  if (!$) return /^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000)$/;
  return new RegExp(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-${$}[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12})$`);
};
var WA = M1(4);
var GA = M1(6);
var UA = M1(7);
var I5 = /^(?!\.)(?!.*\.\.)([A-Za-z0-9_'+\-\.]*)[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$/;
var HA = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;
var KA = /^(([^<>()\[\]\\.,;:\s@"]+(\.[^<>()\[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$/;
var NA = /^[^\s@"]{1,64}@[^\s@]{1,255}$/u;
var VA = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;
var OA = "^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$";
function A5() {
  return new RegExp("^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$", "u");
}
var b5 = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$/;
var P5 = /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|::|([0-9a-fA-F]{1,4})?::([0-9a-fA-F]{1,4}:?){0,6})$/;
var Z5 = /^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\/([0-9]|[1-2][0-9]|3[0-2])$/;
var E5 = /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|::|([0-9a-fA-F]{1,4})?::([0-9a-fA-F]{1,4}:?){0,6})\/(12[0-8]|1[01][0-9]|[1-9]?[0-9])$/;
var R5 = /^$|^(?:[0-9a-zA-Z+/]{4})*(?:(?:[0-9a-zA-Z+/]{2}==)|(?:[0-9a-zA-Z+/]{3}=))?$/;
var aJ = /^[A-Za-z0-9_-]*$/;
var S5 = /^([a-zA-Z0-9-]+\.)*[a-zA-Z0-9-]+$/;
var wA = /^([a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$/;
var v5 = /^\+(?:[0-9]){6,14}[0-9]$/;
var GN = "(?:(?:\\d\\d[2468][048]|\\d\\d[13579][26]|\\d\\d0[48]|[02468][048]00|[13579][26]00)-02-29|\\d{4}-(?:(?:0[13578]|1[02])-(?:0[1-9]|[12]\\d|3[01])|(?:0[469]|11)-(?:0[1-9]|[12]\\d|30)|(?:02)-(?:0[1-9]|1\\d|2[0-8])))";
var C5 = new RegExp(`^${GN}$`);
function UN($) {
  return typeof $.precision === "number" ? $.precision === -1 ? "(?:[01]\\d|2[0-3]):[0-5]\\d" : $.precision === 0 ? "(?:[01]\\d|2[0-3]):[0-5]\\d:[0-5]\\d" : `(?:[01]\\d|2[0-3]):[0-5]\\d:[0-5]\\d\\.\\d{${$.precision}}` : "(?:[01]\\d|2[0-3]):[0-5]\\d(?::[0-5]\\d(?:\\.\\d+)?)?";
}
function k5($) {
  return new RegExp(`^${UN($)}$`);
}
function _5($) {
  let X = UN({ precision: $.precision }), J = ["Z"];
  if ($.local) J.push("");
  if ($.offset) J.push("([+-]\\d{2}:\\d{2})");
  let Q = `${X}(?:${J.join("|")})`;
  return new RegExp(`^${GN}T(?:${Q})$`);
}
var x5 = ($) => {
  let X = $ ? `[\\s\\S]{${$?.minimum ?? 0},${$?.maximum ?? ""}}` : "[\\s\\S]*";
  return new RegExp(`^${X}$`);
};
var T5 = /^\d+n?$/;
var y5 = /^\d+$/;
var f5 = /^-?\d+(?:\.\d+)?/i;
var g5 = /true|false/i;
var h5 = /null/i;
var u5 = /undefined/i;
var m5 = /^[^A-Z]*$/;
var l5 = /^[^a-z]*$/;
var M$ = q("$ZodCheck", ($, X) => {
  var J;
  $._zod ?? ($._zod = {}), $._zod.def = X, (J = $._zod).onattach ?? (J.onattach = []);
});
var KN = { number: "number", bigint: "bigint", object: "date" };
var sJ = q("$ZodCheckLessThan", ($, X) => {
  M$.init($, X);
  let J = KN[typeof X.value];
  $._zod.onattach.push((Q) => {
    let Y = Q._zod.bag, z = (X.inclusive ? Y.maximum : Y.exclusiveMaximum) ?? Number.POSITIVE_INFINITY;
    if (X.value < z) if (X.inclusive) Y.maximum = X.value;
    else Y.exclusiveMaximum = X.value;
  }), $._zod.check = (Q) => {
    if (X.inclusive ? Q.value <= X.value : Q.value < X.value) return;
    Q.issues.push({ origin: J, code: "too_big", maximum: X.value, input: Q.value, inclusive: X.inclusive, inst: $, continue: !X.abort });
  };
});
var eJ = q("$ZodCheckGreaterThan", ($, X) => {
  M$.init($, X);
  let J = KN[typeof X.value];
  $._zod.onattach.push((Q) => {
    let Y = Q._zod.bag, z = (X.inclusive ? Y.minimum : Y.exclusiveMinimum) ?? Number.NEGATIVE_INFINITY;
    if (X.value > z) if (X.inclusive) Y.minimum = X.value;
    else Y.exclusiveMinimum = X.value;
  }), $._zod.check = (Q) => {
    if (X.inclusive ? Q.value >= X.value : Q.value > X.value) return;
    Q.issues.push({ origin: J, code: "too_small", minimum: X.value, input: Q.value, inclusive: X.inclusive, inst: $, continue: !X.abort });
  };
});
var c5 = q("$ZodCheckMultipleOf", ($, X) => {
  M$.init($, X), $._zod.onattach.push((J) => {
    var Q;
    (Q = J._zod.bag).multipleOf ?? (Q.multipleOf = X.value);
  }), $._zod.check = (J) => {
    if (typeof J.value !== typeof X.value) throw Error("Cannot mix number and bigint in multiple_of check.");
    if (typeof J.value === "bigint" ? J.value % X.value === BigInt(0) : W5(J.value, X.value) === 0) return;
    J.issues.push({ origin: typeof J.value, code: "not_multiple_of", divisor: X.value, input: J.value, inst: $, continue: !X.abort });
  };
});
var p5 = q("$ZodCheckNumberFormat", ($, X) => {
  M$.init($, X), X.format = X.format || "float64";
  let J = X.format?.includes("int"), Q = J ? "int" : "number", [Y, z] = N5[X.format];
  $._zod.onattach.push((W) => {
    let G = W._zod.bag;
    if (G.format = X.format, G.minimum = Y, G.maximum = z, J) G.pattern = y5;
  }), $._zod.check = (W) => {
    let G = W.value;
    if (J) {
      if (!Number.isInteger(G)) {
        W.issues.push({ expected: Q, format: X.format, code: "invalid_type", input: G, inst: $ });
        return;
      }
      if (!Number.isSafeInteger(G)) {
        if (G > 0) W.issues.push({ input: G, code: "too_big", maximum: Number.MAX_SAFE_INTEGER, note: "Integers must be within the safe integer range.", inst: $, origin: Q, continue: !X.abort });
        else W.issues.push({ input: G, code: "too_small", minimum: Number.MIN_SAFE_INTEGER, note: "Integers must be within the safe integer range.", inst: $, origin: Q, continue: !X.abort });
        return;
      }
    }
    if (G < Y) W.issues.push({ origin: "number", input: G, code: "too_small", minimum: Y, inclusive: true, inst: $, continue: !X.abort });
    if (G > z) W.issues.push({ origin: "number", input: G, code: "too_big", maximum: z, inst: $ });
  };
});
var i5 = q("$ZodCheckBigIntFormat", ($, X) => {
  M$.init($, X);
  let [J, Q] = V5[X.format];
  $._zod.onattach.push((Y) => {
    let z = Y._zod.bag;
    z.format = X.format, z.minimum = J, z.maximum = Q;
  }), $._zod.check = (Y) => {
    let z = Y.value;
    if (z < J) Y.issues.push({ origin: "bigint", input: z, code: "too_small", minimum: J, inclusive: true, inst: $, continue: !X.abort });
    if (z > Q) Y.issues.push({ origin: "bigint", input: z, code: "too_big", maximum: Q, inst: $ });
  };
});
var n5 = q("$ZodCheckMaxSize", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.size !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag.maximum ?? Number.POSITIVE_INFINITY;
    if (X.maximum < Q) J._zod.bag.maximum = X.maximum;
  }), $._zod.check = (J) => {
    let Q = J.value;
    if (Q.size <= X.maximum) return;
    J.issues.push({ origin: w8(Q), code: "too_big", maximum: X.maximum, input: Q, inst: $, continue: !X.abort });
  };
});
var d5 = q("$ZodCheckMinSize", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.size !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag.minimum ?? Number.NEGATIVE_INFINITY;
    if (X.minimum > Q) J._zod.bag.minimum = X.minimum;
  }), $._zod.check = (J) => {
    let Q = J.value;
    if (Q.size >= X.minimum) return;
    J.issues.push({ origin: w8(Q), code: "too_small", minimum: X.minimum, input: Q, inst: $, continue: !X.abort });
  };
});
var r5 = q("$ZodCheckSizeEquals", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.size !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag;
    Q.minimum = X.size, Q.maximum = X.size, Q.size = X.size;
  }), $._zod.check = (J) => {
    let Q = J.value, Y = Q.size;
    if (Y === X.size) return;
    let z = Y > X.size;
    J.issues.push({ origin: w8(Q), ...z ? { code: "too_big", maximum: X.size } : { code: "too_small", minimum: X.size }, inclusive: true, exact: true, input: J.value, inst: $, continue: !X.abort });
  };
});
var o5 = q("$ZodCheckMaxLength", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.length !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag.maximum ?? Number.POSITIVE_INFINITY;
    if (X.maximum < Q) J._zod.bag.maximum = X.maximum;
  }), $._zod.check = (J) => {
    let Q = J.value;
    if (Q.length <= X.maximum) return;
    let z = B8(Q);
    J.issues.push({ origin: z, code: "too_big", maximum: X.maximum, inclusive: true, input: Q, inst: $, continue: !X.abort });
  };
});
var t5 = q("$ZodCheckMinLength", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.length !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag.minimum ?? Number.NEGATIVE_INFINITY;
    if (X.minimum > Q) J._zod.bag.minimum = X.minimum;
  }), $._zod.check = (J) => {
    let Q = J.value;
    if (Q.length >= X.minimum) return;
    let z = B8(Q);
    J.issues.push({ origin: z, code: "too_small", minimum: X.minimum, inclusive: true, input: Q, inst: $, continue: !X.abort });
  };
});
var a5 = q("$ZodCheckLengthEquals", ($, X) => {
  M$.init($, X), $._zod.when = (J) => {
    let Q = J.value;
    return !C4(Q) && Q.length !== void 0;
  }, $._zod.onattach.push((J) => {
    let Q = J._zod.bag;
    Q.minimum = X.length, Q.maximum = X.length, Q.length = X.length;
  }), $._zod.check = (J) => {
    let Q = J.value, Y = Q.length;
    if (Y === X.length) return;
    let z = B8(Q), W = Y > X.length;
    J.issues.push({ origin: z, ...W ? { code: "too_big", maximum: X.length } : { code: "too_small", minimum: X.length }, inclusive: true, exact: true, input: J.value, inst: $, continue: !X.abort });
  };
});
var q0 = q("$ZodCheckStringFormat", ($, X) => {
  var J, Q;
  if (M$.init($, X), $._zod.onattach.push((Y) => {
    let z = Y._zod.bag;
    if (z.format = X.format, X.pattern) z.patterns ?? (z.patterns = /* @__PURE__ */ new Set()), z.patterns.add(X.pattern);
  }), X.pattern) (J = $._zod).check ?? (J.check = (Y) => {
    if (X.pattern.lastIndex = 0, X.pattern.test(Y.value)) return;
    Y.issues.push({ origin: "string", code: "invalid_format", format: X.format, input: Y.value, ...X.pattern ? { pattern: X.pattern.toString() } : {}, inst: $, continue: !X.abort });
  });
  else (Q = $._zod).check ?? (Q.check = () => {
  });
});
var s5 = q("$ZodCheckRegex", ($, X) => {
  q0.init($, X), $._zod.check = (J) => {
    if (X.pattern.lastIndex = 0, X.pattern.test(J.value)) return;
    J.issues.push({ origin: "string", code: "invalid_format", format: "regex", input: J.value, pattern: X.pattern.toString(), inst: $, continue: !X.abort });
  };
});
var e5 = q("$ZodCheckLowerCase", ($, X) => {
  X.pattern ?? (X.pattern = m5), q0.init($, X);
});
var $W = q("$ZodCheckUpperCase", ($, X) => {
  X.pattern ?? (X.pattern = l5), q0.init($, X);
});
var XW = q("$ZodCheckIncludes", ($, X) => {
  M$.init($, X);
  let J = H4(X.includes), Q = new RegExp(typeof X.position === "number" ? `^.{${X.position}}${J}` : J);
  X.pattern = Q, $._zod.onattach.push((Y) => {
    let z = Y._zod.bag;
    z.patterns ?? (z.patterns = /* @__PURE__ */ new Set()), z.patterns.add(Q);
  }), $._zod.check = (Y) => {
    if (Y.value.includes(X.includes, X.position)) return;
    Y.issues.push({ origin: "string", code: "invalid_format", format: "includes", includes: X.includes, input: Y.value, inst: $, continue: !X.abort });
  };
});
var JW = q("$ZodCheckStartsWith", ($, X) => {
  M$.init($, X);
  let J = new RegExp(`^${H4(X.prefix)}.*`);
  X.pattern ?? (X.pattern = J), $._zod.onattach.push((Q) => {
    let Y = Q._zod.bag;
    Y.patterns ?? (Y.patterns = /* @__PURE__ */ new Set()), Y.patterns.add(J);
  }), $._zod.check = (Q) => {
    if (Q.value.startsWith(X.prefix)) return;
    Q.issues.push({ origin: "string", code: "invalid_format", format: "starts_with", prefix: X.prefix, input: Q.value, inst: $, continue: !X.abort });
  };
});
var YW = q("$ZodCheckEndsWith", ($, X) => {
  M$.init($, X);
  let J = new RegExp(`.*${H4(X.suffix)}$`);
  X.pattern ?? (X.pattern = J), $._zod.onattach.push((Q) => {
    let Y = Q._zod.bag;
    Y.patterns ?? (Y.patterns = /* @__PURE__ */ new Set()), Y.patterns.add(J);
  }), $._zod.check = (Q) => {
    if (Q.value.endsWith(X.suffix)) return;
    Q.issues.push({ origin: "string", code: "invalid_format", format: "ends_with", suffix: X.suffix, input: Q.value, inst: $, continue: !X.abort });
  };
});
function HN($, X, J) {
  if ($.issues.length) X.issues.push(...$6(J, $.issues));
}
var QW = q("$ZodCheckProperty", ($, X) => {
  M$.init($, X), $._zod.check = (J) => {
    let Q = X.schema._zod.run({ value: J.value[X.property], issues: [] }, {});
    if (Q instanceof Promise) return Q.then((Y) => HN(Y, J, X.property));
    HN(Q, J, X.property);
    return;
  };
});
var zW = q("$ZodCheckMimeType", ($, X) => {
  M$.init($, X);
  let J = new Set(X.mime);
  $._zod.onattach.push((Q) => {
    Q._zod.bag.mime = X.mime;
  }), $._zod.check = (Q) => {
    if (J.has(Q.value.type)) return;
    Q.issues.push({ code: "invalid_value", values: X.mime, input: Q.value.type, inst: $ });
  };
});
var WW = q("$ZodCheckOverwrite", ($, X) => {
  M$.init($, X), $._zod.check = (J) => {
    J.value = X.tx(J.value);
  };
});
var $Y = class {
  constructor($ = []) {
    if (this.content = [], this.indent = 0, this) this.args = $;
  }
  indented($) {
    this.indent += 1, $(this), this.indent -= 1;
  }
  write($) {
    if (typeof $ === "function") {
      $(this, { execution: "sync" }), $(this, { execution: "async" });
      return;
    }
    let J = $.split(`
`).filter((z) => z), Q = Math.min(...J.map((z) => z.length - z.trimStart().length)), Y = J.map((z) => z.slice(Q)).map((z) => " ".repeat(this.indent * 2) + z);
    for (let z of Y) this.content.push(z);
  }
  compile() {
    let $ = Function, X = this?.args, Q = [...(this?.content ?? [""]).map((Y) => `  ${Y}`)];
    return new $(...X, Q.join(`
`));
  }
};
var GW = { major: 4, minor: 0, patch: 0 };
var i = q("$ZodType", ($, X) => {
  var J;
  $ ?? ($ = {}), $._zod.def = X, $._zod.bag = $._zod.bag || {}, $._zod.version = GW;
  let Q = [...$._zod.def.checks ?? []];
  if ($._zod.traits.has("$ZodCheck")) Q.unshift($);
  for (let Y of Q) for (let z of Y._zod.onattach) z($);
  if (Q.length === 0) (J = $._zod).deferred ?? (J.deferred = []), $._zod.deferred?.push(() => {
    $._zod.run = $._zod.parse;
  });
  else {
    let Y = (z, W, G) => {
      let U = L1(z), H;
      for (let K of W) {
        if (K._zod.when) {
          if (!K._zod.when(z)) continue;
        } else if (U) continue;
        let V = z.issues.length, O = K._zod.check(z);
        if (O instanceof Promise && G?.async === false) throw new U4();
        if (H || O instanceof Promise) H = (H ?? Promise.resolve()).then(async () => {
          if (await O, z.issues.length === V) return;
          if (!U) U = L1(z, V);
        });
        else {
          if (z.issues.length === V) continue;
          if (!U) U = L1(z, V);
        }
      }
      if (H) return H.then(() => {
        return z;
      });
      return z;
    };
    $._zod.run = (z, W) => {
      let G = $._zod.parse(z, W);
      if (G instanceof Promise) {
        if (W.async === false) throw new U4();
        return G.then((U) => Y(U, Q, W));
      }
      return Y(G, Q, W);
    };
  }
  $["~standard"] = { validate: (Y) => {
    try {
      let z = k4($, Y);
      return z.success ? { value: z.data } : { issues: z.error?.issues };
    } catch (z) {
      return _4($, Y).then((W) => W.success ? { value: W.data } : { issues: W.error?.issues });
    }
  }, vendor: "zod", version: 1 };
});
var T4 = q("$ZodString", ($, X) => {
  i.init($, X), $._zod.pattern = [...$?._zod.bag?.patterns ?? []].pop() ?? x5($._zod.bag), $._zod.parse = (J, Q) => {
    if (X.coerce) try {
      J.value = String(J.value);
    } catch (Y) {
    }
    if (typeof J.value === "string") return J;
    return J.issues.push({ expected: "string", code: "invalid_type", input: J.value, inst: $ }), J;
  };
});
var H$ = q("$ZodStringFormat", ($, X) => {
  q0.init($, X), T4.init($, X);
});
var YY = q("$ZodGUID", ($, X) => {
  X.pattern ?? (X.pattern = M5), H$.init($, X);
});
var QY = q("$ZodUUID", ($, X) => {
  if (X.version) {
    let Q = { v1: 1, v2: 2, v3: 3, v4: 4, v5: 5, v6: 6, v7: 7, v8: 8 }[X.version];
    if (Q === void 0) throw Error(`Invalid UUID version: "${X.version}"`);
    X.pattern ?? (X.pattern = M1(Q));
  } else X.pattern ?? (X.pattern = M1());
  H$.init($, X);
});
var zY = q("$ZodEmail", ($, X) => {
  X.pattern ?? (X.pattern = I5), H$.init($, X);
});
var WY = q("$ZodURL", ($, X) => {
  H$.init($, X), $._zod.check = (J) => {
    try {
      let Q = J.value, Y = new URL(Q), z = Y.href;
      if (X.hostname) {
        if (X.hostname.lastIndex = 0, !X.hostname.test(Y.hostname)) J.issues.push({ code: "invalid_format", format: "url", note: "Invalid hostname", pattern: S5.source, input: J.value, inst: $, continue: !X.abort });
      }
      if (X.protocol) {
        if (X.protocol.lastIndex = 0, !X.protocol.test(Y.protocol.endsWith(":") ? Y.protocol.slice(0, -1) : Y.protocol)) J.issues.push({ code: "invalid_format", format: "url", note: "Invalid protocol", pattern: X.protocol.source, input: J.value, inst: $, continue: !X.abort });
      }
      if (!Q.endsWith("/") && z.endsWith("/")) J.value = z.slice(0, -1);
      else J.value = z;
      return;
    } catch (Q) {
      J.issues.push({ code: "invalid_format", format: "url", input: J.value, inst: $, continue: !X.abort });
    }
  };
});
var GY = q("$ZodEmoji", ($, X) => {
  X.pattern ?? (X.pattern = A5()), H$.init($, X);
});
var UY = q("$ZodNanoID", ($, X) => {
  X.pattern ?? (X.pattern = j5), H$.init($, X);
});
var HY = q("$ZodCUID", ($, X) => {
  X.pattern ?? (X.pattern = w5), H$.init($, X);
});
var KY = q("$ZodCUID2", ($, X) => {
  X.pattern ?? (X.pattern = B5), H$.init($, X);
});
var NY = q("$ZodULID", ($, X) => {
  X.pattern ?? (X.pattern = q5), H$.init($, X);
});
var VY = q("$ZodXID", ($, X) => {
  X.pattern ?? (X.pattern = D5), H$.init($, X);
});
var OY = q("$ZodKSUID", ($, X) => {
  X.pattern ?? (X.pattern = L5), H$.init($, X);
});
var HW = q("$ZodISODateTime", ($, X) => {
  X.pattern ?? (X.pattern = _5(X)), H$.init($, X);
});
var KW = q("$ZodISODate", ($, X) => {
  X.pattern ?? (X.pattern = C5), H$.init($, X);
});
var NW = q("$ZodISOTime", ($, X) => {
  X.pattern ?? (X.pattern = k5(X)), H$.init($, X);
});
var VW = q("$ZodISODuration", ($, X) => {
  X.pattern ?? (X.pattern = F5), H$.init($, X);
});
var wY = q("$ZodIPv4", ($, X) => {
  X.pattern ?? (X.pattern = b5), H$.init($, X), $._zod.onattach.push((J) => {
    let Q = J._zod.bag;
    Q.format = "ipv4";
  });
});
var BY = q("$ZodIPv6", ($, X) => {
  X.pattern ?? (X.pattern = P5), H$.init($, X), $._zod.onattach.push((J) => {
    let Q = J._zod.bag;
    Q.format = "ipv6";
  }), $._zod.check = (J) => {
    try {
      new URL(`http://[${J.value}]`);
    } catch {
      J.issues.push({ code: "invalid_format", format: "ipv6", input: J.value, inst: $, continue: !X.abort });
    }
  };
});
var qY = q("$ZodCIDRv4", ($, X) => {
  X.pattern ?? (X.pattern = Z5), H$.init($, X);
});
var DY = q("$ZodCIDRv6", ($, X) => {
  X.pattern ?? (X.pattern = E5), H$.init($, X), $._zod.check = (J) => {
    let [Q, Y] = J.value.split("/");
    try {
      if (!Y) throw Error();
      let z = Number(Y);
      if (`${z}` !== Y) throw Error();
      if (z < 0 || z > 128) throw Error();
      new URL(`http://[${Q}]`);
    } catch {
      J.issues.push({ code: "invalid_format", format: "cidrv6", input: J.value, inst: $, continue: !X.abort });
    }
  };
});
function OW($) {
  if ($ === "") return true;
  if ($.length % 4 !== 0) return false;
  try {
    return atob($), true;
  } catch {
    return false;
  }
}
var LY = q("$ZodBase64", ($, X) => {
  X.pattern ?? (X.pattern = R5), H$.init($, X), $._zod.onattach.push((J) => {
    J._zod.bag.contentEncoding = "base64";
  }), $._zod.check = (J) => {
    if (OW(J.value)) return;
    J.issues.push({ code: "invalid_format", format: "base64", input: J.value, inst: $, continue: !X.abort });
  };
});
function AN($) {
  if (!aJ.test($)) return false;
  let X = $.replace(/[-_]/g, (Q) => Q === "-" ? "+" : "/"), J = X.padEnd(Math.ceil(X.length / 4) * 4, "=");
  return OW(J);
}
var jY = q("$ZodBase64URL", ($, X) => {
  X.pattern ?? (X.pattern = aJ), H$.init($, X), $._zod.onattach.push((J) => {
    J._zod.bag.contentEncoding = "base64url";
  }), $._zod.check = (J) => {
    if (AN(J.value)) return;
    J.issues.push({ code: "invalid_format", format: "base64url", input: J.value, inst: $, continue: !X.abort });
  };
});
var FY = q("$ZodE164", ($, X) => {
  X.pattern ?? (X.pattern = v5), H$.init($, X);
});
function bN($, X = null) {
  try {
    let J = $.split(".");
    if (J.length !== 3) return false;
    let [Q] = J;
    if (!Q) return false;
    let Y = JSON.parse(atob(Q));
    if ("typ" in Y && Y?.typ !== "JWT") return false;
    if (!Y.alg) return false;
    if (X && (!("alg" in Y) || Y.alg !== X)) return false;
    return true;
  } catch {
    return false;
  }
}
var MY = q("$ZodJWT", ($, X) => {
  H$.init($, X), $._zod.check = (J) => {
    if (bN(J.value, X.alg)) return;
    J.issues.push({ code: "invalid_format", format: "jwt", input: J.value, inst: $, continue: !X.abort });
  };
});
var IY = q("$ZodCustomStringFormat", ($, X) => {
  H$.init($, X), $._zod.check = (J) => {
    if (X.fn(J.value)) return;
    J.issues.push({ code: "invalid_format", format: X.format, input: J.value, inst: $, continue: !X.abort });
  };
});
var D8 = q("$ZodNumber", ($, X) => {
  i.init($, X), $._zod.pattern = $._zod.bag.pattern ?? f5, $._zod.parse = (J, Q) => {
    if (X.coerce) try {
      J.value = Number(J.value);
    } catch (W) {
    }
    let Y = J.value;
    if (typeof Y === "number" && !Number.isNaN(Y) && Number.isFinite(Y)) return J;
    let z = typeof Y === "number" ? Number.isNaN(Y) ? "NaN" : !Number.isFinite(Y) ? "Infinity" : void 0 : void 0;
    return J.issues.push({ expected: "number", code: "invalid_type", input: Y, inst: $, ...z ? { received: z } : {} }), J;
  };
});
var AY = q("$ZodNumber", ($, X) => {
  p5.init($, X), D8.init($, X);
});
var D0 = q("$ZodBoolean", ($, X) => {
  i.init($, X), $._zod.pattern = g5, $._zod.parse = (J, Q) => {
    if (X.coerce) try {
      J.value = Boolean(J.value);
    } catch (z) {
    }
    let Y = J.value;
    if (typeof Y === "boolean") return J;
    return J.issues.push({ expected: "boolean", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var L8 = q("$ZodBigInt", ($, X) => {
  i.init($, X), $._zod.pattern = T5, $._zod.parse = (J, Q) => {
    if (X.coerce) try {
      J.value = BigInt(J.value);
    } catch (Y) {
    }
    if (typeof J.value === "bigint") return J;
    return J.issues.push({ expected: "bigint", code: "invalid_type", input: J.value, inst: $ }), J;
  };
});
var bY = q("$ZodBigInt", ($, X) => {
  i5.init($, X), L8.init($, X);
});
var PY = q("$ZodSymbol", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (typeof Y === "symbol") return J;
    return J.issues.push({ expected: "symbol", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var ZY = q("$ZodUndefined", ($, X) => {
  i.init($, X), $._zod.pattern = u5, $._zod.values = /* @__PURE__ */ new Set([void 0]), $._zod.optin = "optional", $._zod.optout = "optional", $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (typeof Y > "u") return J;
    return J.issues.push({ expected: "undefined", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var EY = q("$ZodNull", ($, X) => {
  i.init($, X), $._zod.pattern = h5, $._zod.values = /* @__PURE__ */ new Set([null]), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (Y === null) return J;
    return J.issues.push({ expected: "null", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var RY = q("$ZodAny", ($, X) => {
  i.init($, X), $._zod.parse = (J) => J;
});
var I1 = q("$ZodUnknown", ($, X) => {
  i.init($, X), $._zod.parse = (J) => J;
});
var SY = q("$ZodNever", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    return J.issues.push({ expected: "never", code: "invalid_type", input: J.value, inst: $ }), J;
  };
});
var vY = q("$ZodVoid", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (typeof Y > "u") return J;
    return J.issues.push({ expected: "void", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var CY = q("$ZodDate", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    if (X.coerce) try {
      J.value = new Date(J.value);
    } catch (G) {
    }
    let Y = J.value, z = Y instanceof Date;
    if (z && !Number.isNaN(Y.getTime())) return J;
    return J.issues.push({ expected: "date", code: "invalid_type", input: Y, ...z ? { received: "Invalid Date" } : {}, inst: $ }), J;
  };
});
function VN($, X, J) {
  if ($.issues.length) X.issues.push(...$6(J, $.issues));
  X.value[J] = $.value;
}
var L0 = q("$ZodArray", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (!Array.isArray(Y)) return J.issues.push({ expected: "array", code: "invalid_type", input: Y, inst: $ }), J;
    J.value = Array(Y.length);
    let z = [];
    for (let W = 0; W < Y.length; W++) {
      let G = Y[W], U = X.element._zod.run({ value: G, issues: [] }, Q);
      if (U instanceof Promise) z.push(U.then((H) => VN(H, J, W)));
      else VN(U, J, W);
    }
    if (z.length) return Promise.all(z).then(() => J);
    return J;
  };
});
function XY($, X, J) {
  if ($.issues.length) X.issues.push(...$6(J, $.issues));
  X.value[J] = $.value;
}
function ON($, X, J, Q) {
  if ($.issues.length) if (Q[J] === void 0) if (J in Q) X.value[J] = void 0;
  else X.value[J] = $.value;
  else X.issues.push(...$6(J, $.issues));
  else if ($.value === void 0) {
    if (J in Q) X.value[J] = void 0;
  } else X.value[J] = $.value;
}
var j8 = q("$ZodObject", ($, X) => {
  i.init($, X);
  let J = N8(() => {
    let V = Object.keys(X.shape);
    for (let N of V) if (!(X.shape[N] instanceof i)) throw Error(`Invalid element at key "${N}": expected a Zod schema`);
    let O = K5(X.shape);
    return { shape: X.shape, keys: V, keySet: new Set(V), numKeys: V.length, optionalKeys: new Set(O) };
  });
  W$($._zod, "propValues", () => {
    let V = X.shape, O = {};
    for (let N in V) {
      let w = V[N]._zod;
      if (w.values) {
        O[N] ?? (O[N] = /* @__PURE__ */ new Set());
        for (let B of w.values) O[N].add(B);
      }
    }
    return O;
  });
  let Q = (V) => {
    let O = new $Y(["shape", "payload", "ctx"]), N = J.value, w = (I) => {
      let b = D1(I);
      return `shape[${b}]._zod.run({ value: input[${b}], issues: [] }, ctx)`;
    };
    O.write("const input = payload.value;");
    let B = /* @__PURE__ */ Object.create(null), L = 0;
    for (let I of N.keys) B[I] = `key_${L++}`;
    O.write("const newResult = {}");
    for (let I of N.keys) if (N.optionalKeys.has(I)) {
      let b = B[I];
      O.write(`const ${b} = ${w(I)};`);
      let x = D1(I);
      O.write(`
        if (${b}.issues.length) {
          if (input[${x}] === undefined) {
            if (${x} in input) {
              newResult[${x}] = undefined;
            }
          } else {
            payload.issues = payload.issues.concat(
              ${b}.issues.map((iss) => ({
                ...iss,
                path: iss.path ? [${x}, ...iss.path] : [${x}],
              }))
            );
          }
        } else if (${b}.value === undefined) {
          if (${x} in input) newResult[${x}] = undefined;
        } else {
          newResult[${x}] = ${b}.value;
        }
        `);
    } else {
      let b = B[I];
      O.write(`const ${b} = ${w(I)};`), O.write(`
          if (${b}.issues.length) payload.issues = payload.issues.concat(${b}.issues.map(iss => ({
            ...iss,
            path: iss.path ? [${D1(I)}, ...iss.path] : [${D1(I)}]
          })));`), O.write(`newResult[${D1(I)}] = ${b}.value`);
    }
    O.write("payload.value = newResult;"), O.write("return payload;");
    let j = O.compile();
    return (I, b) => j(V, I, b);
  }, Y, z = N0, W = !U8.jitless, U = W && U5.value, H = X.catchall, K;
  $._zod.parse = (V, O) => {
    K ?? (K = J.value);
    let N = V.value;
    if (!z(N)) return V.issues.push({ expected: "object", code: "invalid_type", input: N, inst: $ }), V;
    let w = [];
    if (W && U && O?.async === false && O.jitless !== true) {
      if (!Y) Y = Q(X.shape);
      V = Y(V, O);
    } else {
      V.value = {};
      let b = K.shape;
      for (let x of K.keys) {
        let h = b[x], B$ = h._zod.run({ value: N[x], issues: [] }, O), x$ = h._zod.optin === "optional" && h._zod.optout === "optional";
        if (B$ instanceof Promise) w.push(B$.then((G6) => x$ ? ON(G6, V, x, N) : XY(G6, V, x)));
        else if (x$) ON(B$, V, x, N);
        else XY(B$, V, x);
      }
    }
    if (!H) return w.length ? Promise.all(w).then(() => V) : V;
    let B = [], L = K.keySet, j = H._zod, I = j.def.type;
    for (let b of Object.keys(N)) {
      if (L.has(b)) continue;
      if (I === "never") {
        B.push(b);
        continue;
      }
      let x = j.run({ value: N[b], issues: [] }, O);
      if (x instanceof Promise) w.push(x.then((h) => XY(h, V, b)));
      else XY(x, V, b);
    }
    if (B.length) V.issues.push({ code: "unrecognized_keys", keys: B, input: N, inst: $ });
    if (!w.length) return V;
    return Promise.all(w).then(() => {
      return V;
    });
  };
});
function wN($, X, J, Q) {
  for (let Y of $) if (Y.issues.length === 0) return X.value = Y.value, X;
  return X.issues.push({ code: "invalid_union", input: X.value, inst: J, errors: $.map((Y) => Y.issues.map((z) => O6(z, Q, E$()))) }), X;
}
var F8 = q("$ZodUnion", ($, X) => {
  i.init($, X), W$($._zod, "optin", () => X.options.some((J) => J._zod.optin === "optional") ? "optional" : void 0), W$($._zod, "optout", () => X.options.some((J) => J._zod.optout === "optional") ? "optional" : void 0), W$($._zod, "values", () => {
    if (X.options.every((J) => J._zod.values)) return new Set(X.options.flatMap((J) => Array.from(J._zod.values)));
    return;
  }), W$($._zod, "pattern", () => {
    if (X.options.every((J) => J._zod.pattern)) {
      let J = X.options.map((Q) => Q._zod.pattern);
      return new RegExp(`^(${J.map((Q) => V8(Q.source)).join("|")})$`);
    }
    return;
  }), $._zod.parse = (J, Q) => {
    let Y = false, z = [];
    for (let W of X.options) {
      let G = W._zod.run({ value: J.value, issues: [] }, Q);
      if (G instanceof Promise) z.push(G), Y = true;
      else {
        if (G.issues.length === 0) return G;
        z.push(G);
      }
    }
    if (!Y) return wN(z, J, $, Q);
    return Promise.all(z).then((W) => {
      return wN(W, J, $, Q);
    });
  };
});
var kY = q("$ZodDiscriminatedUnion", ($, X) => {
  F8.init($, X);
  let J = $._zod.parse;
  W$($._zod, "propValues", () => {
    let Y = {};
    for (let z of X.options) {
      let W = z._zod.propValues;
      if (!W || Object.keys(W).length === 0) throw Error(`Invalid discriminated union option at index "${X.options.indexOf(z)}"`);
      for (let [G, U] of Object.entries(W)) {
        if (!Y[G]) Y[G] = /* @__PURE__ */ new Set();
        for (let H of U) Y[G].add(H);
      }
    }
    return Y;
  });
  let Q = N8(() => {
    let Y = X.options, z = /* @__PURE__ */ new Map();
    for (let W of Y) {
      let G = W._zod.propValues[X.discriminator];
      if (!G || G.size === 0) throw Error(`Invalid discriminated union option at index "${X.options.indexOf(W)}"`);
      for (let U of G) {
        if (z.has(U)) throw Error(`Duplicate discriminator value "${String(U)}"`);
        z.set(U, W);
      }
    }
    return z;
  });
  $._zod.parse = (Y, z) => {
    let W = Y.value;
    if (!N0(W)) return Y.issues.push({ code: "invalid_type", expected: "object", input: W, inst: $ }), Y;
    let G = Q.value.get(W?.[X.discriminator]);
    if (G) return G._zod.run(Y, z);
    if (X.unionFallback) return J(Y, z);
    return Y.issues.push({ code: "invalid_union", errors: [], note: "No matching discriminator", input: W, path: [X.discriminator], inst: $ }), Y;
  };
});
var _Y = q("$ZodIntersection", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value, z = X.left._zod.run({ value: Y, issues: [] }, Q), W = X.right._zod.run({ value: Y, issues: [] }, Q);
    if (z instanceof Promise || W instanceof Promise) return Promise.all([z, W]).then(([U, H]) => {
      return BN(J, U, H);
    });
    return BN(J, z, W);
  };
});
function UW($, X) {
  if ($ === X) return { valid: true, data: $ };
  if ($ instanceof Date && X instanceof Date && +$ === +X) return { valid: true, data: $ };
  if (V0($) && V0(X)) {
    let J = Object.keys(X), Q = Object.keys($).filter((z) => J.indexOf(z) !== -1), Y = { ...$, ...X };
    for (let z of Q) {
      let W = UW($[z], X[z]);
      if (!W.valid) return { valid: false, mergeErrorPath: [z, ...W.mergeErrorPath] };
      Y[z] = W.data;
    }
    return { valid: true, data: Y };
  }
  if (Array.isArray($) && Array.isArray(X)) {
    if ($.length !== X.length) return { valid: false, mergeErrorPath: [] };
    let J = [];
    for (let Q = 0; Q < $.length; Q++) {
      let Y = $[Q], z = X[Q], W = UW(Y, z);
      if (!W.valid) return { valid: false, mergeErrorPath: [Q, ...W.mergeErrorPath] };
      J.push(W.data);
    }
    return { valid: true, data: J };
  }
  return { valid: false, mergeErrorPath: [] };
}
function BN($, X, J) {
  if (X.issues.length) $.issues.push(...X.issues);
  if (J.issues.length) $.issues.push(...J.issues);
  if (L1($)) return $;
  let Q = UW(X.value, J.value);
  if (!Q.valid) throw Error(`Unmergable intersection. Error path: ${JSON.stringify(Q.mergeErrorPath)}`);
  return $.value = Q.data, $;
}
var y4 = q("$ZodTuple", ($, X) => {
  i.init($, X);
  let J = X.items, Q = J.length - [...J].reverse().findIndex((Y) => Y._zod.optin !== "optional");
  $._zod.parse = (Y, z) => {
    let W = Y.value;
    if (!Array.isArray(W)) return Y.issues.push({ input: W, inst: $, expected: "tuple", code: "invalid_type" }), Y;
    Y.value = [];
    let G = [];
    if (!X.rest) {
      let H = W.length > J.length, K = W.length < Q - 1;
      if (H || K) return Y.issues.push({ input: W, inst: $, origin: "array", ...H ? { code: "too_big", maximum: J.length } : { code: "too_small", minimum: J.length } }), Y;
    }
    let U = -1;
    for (let H of J) {
      if (U++, U >= W.length) {
        if (U >= Q) continue;
      }
      let K = H._zod.run({ value: W[U], issues: [] }, z);
      if (K instanceof Promise) G.push(K.then((V) => JY(V, Y, U)));
      else JY(K, Y, U);
    }
    if (X.rest) {
      let H = W.slice(J.length);
      for (let K of H) {
        U++;
        let V = X.rest._zod.run({ value: K, issues: [] }, z);
        if (V instanceof Promise) G.push(V.then((O) => JY(O, Y, U)));
        else JY(V, Y, U);
      }
    }
    if (G.length) return Promise.all(G).then(() => Y);
    return Y;
  };
});
function JY($, X, J) {
  if ($.issues.length) X.issues.push(...$6(J, $.issues));
  X.value[J] = $.value;
}
var xY = q("$ZodRecord", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (!V0(Y)) return J.issues.push({ expected: "record", code: "invalid_type", input: Y, inst: $ }), J;
    let z = [];
    if (X.keyType._zod.values) {
      let W = X.keyType._zod.values;
      J.value = {};
      for (let U of W) if (typeof U === "string" || typeof U === "number" || typeof U === "symbol") {
        let H = X.valueType._zod.run({ value: Y[U], issues: [] }, Q);
        if (H instanceof Promise) z.push(H.then((K) => {
          if (K.issues.length) J.issues.push(...$6(U, K.issues));
          J.value[U] = K.value;
        }));
        else {
          if (H.issues.length) J.issues.push(...$6(U, H.issues));
          J.value[U] = H.value;
        }
      }
      let G;
      for (let U in Y) if (!W.has(U)) G = G ?? [], G.push(U);
      if (G && G.length > 0) J.issues.push({ code: "unrecognized_keys", input: Y, inst: $, keys: G });
    } else {
      J.value = {};
      for (let W of Reflect.ownKeys(Y)) {
        if (W === "__proto__") continue;
        let G = X.keyType._zod.run({ value: W, issues: [] }, Q);
        if (G instanceof Promise) throw Error("Async schemas not supported in object keys currently");
        if (G.issues.length) {
          J.issues.push({ origin: "record", code: "invalid_key", issues: G.issues.map((H) => O6(H, Q, E$())), input: W, path: [W], inst: $ }), J.value[G.value] = G.value;
          continue;
        }
        let U = X.valueType._zod.run({ value: Y[W], issues: [] }, Q);
        if (U instanceof Promise) z.push(U.then((H) => {
          if (H.issues.length) J.issues.push(...$6(W, H.issues));
          J.value[G.value] = H.value;
        }));
        else {
          if (U.issues.length) J.issues.push(...$6(W, U.issues));
          J.value[G.value] = U.value;
        }
      }
    }
    if (z.length) return Promise.all(z).then(() => J);
    return J;
  };
});
var TY = q("$ZodMap", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (!(Y instanceof Map)) return J.issues.push({ expected: "map", code: "invalid_type", input: Y, inst: $ }), J;
    let z = [];
    J.value = /* @__PURE__ */ new Map();
    for (let [W, G] of Y) {
      let U = X.keyType._zod.run({ value: W, issues: [] }, Q), H = X.valueType._zod.run({ value: G, issues: [] }, Q);
      if (U instanceof Promise || H instanceof Promise) z.push(Promise.all([U, H]).then(([K, V]) => {
        qN(K, V, J, W, Y, $, Q);
      }));
      else qN(U, H, J, W, Y, $, Q);
    }
    if (z.length) return Promise.all(z).then(() => J);
    return J;
  };
});
function qN($, X, J, Q, Y, z, W) {
  if ($.issues.length) if (O8.has(typeof Q)) J.issues.push(...$6(Q, $.issues));
  else J.issues.push({ origin: "map", code: "invalid_key", input: Y, inst: z, issues: $.issues.map((G) => O6(G, W, E$())) });
  if (X.issues.length) if (O8.has(typeof Q)) J.issues.push(...$6(Q, X.issues));
  else J.issues.push({ origin: "map", code: "invalid_element", input: Y, inst: z, key: Q, issues: X.issues.map((G) => O6(G, W, E$())) });
  J.value.set($.value, X.value);
}
var yY = q("$ZodSet", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (!(Y instanceof Set)) return J.issues.push({ input: Y, inst: $, expected: "set", code: "invalid_type" }), J;
    let z = [];
    J.value = /* @__PURE__ */ new Set();
    for (let W of Y) {
      let G = X.valueType._zod.run({ value: W, issues: [] }, Q);
      if (G instanceof Promise) z.push(G.then((U) => DN(U, J)));
      else DN(G, J);
    }
    if (z.length) return Promise.all(z).then(() => J);
    return J;
  };
});
function DN($, X) {
  if ($.issues.length) X.issues.push(...$.issues);
  X.value.add($.value);
}
var fY = q("$ZodEnum", ($, X) => {
  i.init($, X);
  let J = K8(X.entries);
  $._zod.values = new Set(J), $._zod.pattern = new RegExp(`^(${J.filter((Q) => O8.has(typeof Q)).map((Q) => typeof Q === "string" ? H4(Q) : Q.toString()).join("|")})$`), $._zod.parse = (Q, Y) => {
    let z = Q.value;
    if ($._zod.values.has(z)) return Q;
    return Q.issues.push({ code: "invalid_value", values: J, input: z, inst: $ }), Q;
  };
});
var gY = q("$ZodLiteral", ($, X) => {
  i.init($, X), $._zod.values = new Set(X.values), $._zod.pattern = new RegExp(`^(${X.values.map((J) => typeof J === "string" ? H4(J) : J ? J.toString() : String(J)).join("|")})$`), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if ($._zod.values.has(Y)) return J;
    return J.issues.push({ code: "invalid_value", values: X.values, input: Y, inst: $ }), J;
  };
});
var hY = q("$ZodFile", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = J.value;
    if (Y instanceof File) return J;
    return J.issues.push({ expected: "file", code: "invalid_type", input: Y, inst: $ }), J;
  };
});
var j0 = q("$ZodTransform", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = X.transform(J.value, J);
    if (Q.async) return (Y instanceof Promise ? Y : Promise.resolve(Y)).then((W) => {
      return J.value = W, J;
    });
    if (Y instanceof Promise) throw new U4();
    return J.value = Y, J;
  };
});
var uY = q("$ZodOptional", ($, X) => {
  i.init($, X), $._zod.optin = "optional", $._zod.optout = "optional", W$($._zod, "values", () => {
    return X.innerType._zod.values ? /* @__PURE__ */ new Set([...X.innerType._zod.values, void 0]) : void 0;
  }), W$($._zod, "pattern", () => {
    let J = X.innerType._zod.pattern;
    return J ? new RegExp(`^(${V8(J.source)})?$`) : void 0;
  }), $._zod.parse = (J, Q) => {
    if (X.innerType._zod.optin === "optional") return X.innerType._zod.run(J, Q);
    if (J.value === void 0) return J;
    return X.innerType._zod.run(J, Q);
  };
});
var mY = q("$ZodNullable", ($, X) => {
  i.init($, X), W$($._zod, "optin", () => X.innerType._zod.optin), W$($._zod, "optout", () => X.innerType._zod.optout), W$($._zod, "pattern", () => {
    let J = X.innerType._zod.pattern;
    return J ? new RegExp(`^(${V8(J.source)}|null)$`) : void 0;
  }), W$($._zod, "values", () => {
    return X.innerType._zod.values ? /* @__PURE__ */ new Set([...X.innerType._zod.values, null]) : void 0;
  }), $._zod.parse = (J, Q) => {
    if (J.value === null) return J;
    return X.innerType._zod.run(J, Q);
  };
});
var lY = q("$ZodDefault", ($, X) => {
  i.init($, X), $._zod.optin = "optional", W$($._zod, "values", () => X.innerType._zod.values), $._zod.parse = (J, Q) => {
    if (J.value === void 0) return J.value = X.defaultValue, J;
    let Y = X.innerType._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then((z) => LN(z, X));
    return LN(Y, X);
  };
});
function LN($, X) {
  if ($.value === void 0) $.value = X.defaultValue;
  return $;
}
var cY = q("$ZodPrefault", ($, X) => {
  i.init($, X), $._zod.optin = "optional", W$($._zod, "values", () => X.innerType._zod.values), $._zod.parse = (J, Q) => {
    if (J.value === void 0) J.value = X.defaultValue;
    return X.innerType._zod.run(J, Q);
  };
});
var pY = q("$ZodNonOptional", ($, X) => {
  i.init($, X), W$($._zod, "values", () => {
    let J = X.innerType._zod.values;
    return J ? new Set([...J].filter((Q) => Q !== void 0)) : void 0;
  }), $._zod.parse = (J, Q) => {
    let Y = X.innerType._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then((z) => jN(z, $));
    return jN(Y, $);
  };
});
function jN($, X) {
  if (!$.issues.length && $.value === void 0) $.issues.push({ code: "invalid_type", expected: "nonoptional", input: $.value, inst: X });
  return $;
}
var iY = q("$ZodSuccess", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    let Y = X.innerType._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then((z) => {
      return J.value = z.issues.length === 0, J;
    });
    return J.value = Y.issues.length === 0, J;
  };
});
var nY = q("$ZodCatch", ($, X) => {
  i.init($, X), $._zod.optin = "optional", W$($._zod, "optout", () => X.innerType._zod.optout), W$($._zod, "values", () => X.innerType._zod.values), $._zod.parse = (J, Q) => {
    let Y = X.innerType._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then((z) => {
      if (J.value = z.value, z.issues.length) J.value = X.catchValue({ ...J, error: { issues: z.issues.map((W) => O6(W, Q, E$())) }, input: J.value }), J.issues = [];
      return J;
    });
    if (J.value = Y.value, Y.issues.length) J.value = X.catchValue({ ...J, error: { issues: Y.issues.map((z) => O6(z, Q, E$())) }, input: J.value }), J.issues = [];
    return J;
  };
});
var dY = q("$ZodNaN", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    if (typeof J.value !== "number" || !Number.isNaN(J.value)) return J.issues.push({ input: J.value, inst: $, expected: "nan", code: "invalid_type" }), J;
    return J;
  };
});
var F0 = q("$ZodPipe", ($, X) => {
  i.init($, X), W$($._zod, "values", () => X.in._zod.values), W$($._zod, "optin", () => X.in._zod.optin), W$($._zod, "optout", () => X.out._zod.optout), $._zod.parse = (J, Q) => {
    let Y = X.in._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then((z) => FN(z, X, Q));
    return FN(Y, X, Q);
  };
});
function FN($, X, J) {
  if (L1($)) return $;
  return X.out._zod.run({ value: $.value, issues: $.issues }, J);
}
var rY = q("$ZodReadonly", ($, X) => {
  i.init($, X), W$($._zod, "propValues", () => X.innerType._zod.propValues), W$($._zod, "values", () => X.innerType._zod.values), W$($._zod, "optin", () => X.innerType._zod.optin), W$($._zod, "optout", () => X.innerType._zod.optout), $._zod.parse = (J, Q) => {
    let Y = X.innerType._zod.run(J, Q);
    if (Y instanceof Promise) return Y.then(MN);
    return MN(Y);
  };
});
function MN($) {
  return $.value = Object.freeze($.value), $;
}
var oY = q("$ZodTemplateLiteral", ($, X) => {
  i.init($, X);
  let J = [];
  for (let Q of X.parts) if (Q instanceof i) {
    if (!Q._zod.pattern) throw Error(`Invalid template literal part, no pattern found: ${[...Q._zod.traits].shift()}`);
    let Y = Q._zod.pattern instanceof RegExp ? Q._zod.pattern.source : Q._zod.pattern;
    if (!Y) throw Error(`Invalid template literal part: ${Q._zod.traits}`);
    let z = Y.startsWith("^") ? 1 : 0, W = Y.endsWith("$") ? Y.length - 1 : Y.length;
    J.push(Y.slice(z, W));
  } else if (Q === null || H5.has(typeof Q)) J.push(H4(`${Q}`));
  else throw Error(`Invalid template literal part: ${Q}`);
  $._zod.pattern = new RegExp(`^${J.join("")}$`), $._zod.parse = (Q, Y) => {
    if (typeof Q.value !== "string") return Q.issues.push({ input: Q.value, inst: $, expected: "template_literal", code: "invalid_type" }), Q;
    if ($._zod.pattern.lastIndex = 0, !$._zod.pattern.test(Q.value)) return Q.issues.push({ input: Q.value, inst: $, code: "invalid_format", format: "template_literal", pattern: $._zod.pattern.source }), Q;
    return Q;
  };
});
var tY = q("$ZodPromise", ($, X) => {
  i.init($, X), $._zod.parse = (J, Q) => {
    return Promise.resolve(J.value).then((Y) => X.innerType._zod.run({ value: Y, issues: [] }, Q));
  };
});
var aY = q("$ZodLazy", ($, X) => {
  i.init($, X), W$($._zod, "innerType", () => X.getter()), W$($._zod, "pattern", () => $._zod.innerType._zod.pattern), W$($._zod, "propValues", () => $._zod.innerType._zod.propValues), W$($._zod, "optin", () => $._zod.innerType._zod.optin), W$($._zod, "optout", () => $._zod.innerType._zod.optout), $._zod.parse = (J, Q) => {
    return $._zod.innerType._zod.run(J, Q);
  };
});
var sY = q("$ZodCustom", ($, X) => {
  M$.init($, X), i.init($, X), $._zod.parse = (J, Q) => {
    return J;
  }, $._zod.check = (J) => {
    let Q = J.value, Y = X.fn(Q);
    if (Y instanceof Promise) return Y.then((z) => IN(z, J, Q, $));
    IN(Y, J, Q, $);
    return;
  };
});
function IN($, X, J, Q) {
  if (!$) {
    let Y = { code: "custom", input: J, inst: Q, path: [...Q._zod.def.path ?? []], continue: !Q._zod.def.abort };
    if (Q._zod.def.params) Y.params = Q._zod.def.params;
    X.issues.push(O5(Y));
  }
}
var M0 = {};
$1(M0, { zhTW: () => aW, zhCN: () => tW, vi: () => oW, ur: () => rW, ua: () => dW, tr: () => nW, th: () => iW, ta: () => pW, sv: () => cW, sl: () => lW, ru: () => mW, pt: () => uW, ps: () => gW, pl: () => hW, ota: () => fW, no: () => yW, nl: () => TW, ms: () => xW, mk: () => _W, ko: () => kW, kh: () => CW, ja: () => vW, it: () => SW, id: () => RW, hu: () => EW, he: () => ZW, frCA: () => PW, fr: () => bW, fi: () => AW, fa: () => IW, es: () => MW, eo: () => FW, en: () => M8, de: () => jW, cs: () => LW, ca: () => DW, be: () => qW, az: () => BW, ar: () => wW });
var BA = () => {
  let $ = { string: { unit: "\u062D\u0631\u0641", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, file: { unit: "\u0628\u0627\u064A\u062A", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, array: { unit: "\u0639\u0646\u0635\u0631", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, set: { unit: "\u0639\u0646\u0635\u0631", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0645\u062F\u062E\u0644", email: "\u0628\u0631\u064A\u062F \u0625\u0644\u0643\u062A\u0631\u0648\u0646\u064A", url: "\u0631\u0627\u0628\u0637", emoji: "\u0625\u064A\u0645\u0648\u062C\u064A", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u062A\u0627\u0631\u064A\u062E \u0648\u0648\u0642\u062A \u0628\u0645\u0639\u064A\u0627\u0631 ISO", date: "\u062A\u0627\u0631\u064A\u062E \u0628\u0645\u0639\u064A\u0627\u0631 ISO", time: "\u0648\u0642\u062A \u0628\u0645\u0639\u064A\u0627\u0631 ISO", duration: "\u0645\u062F\u0629 \u0628\u0645\u0639\u064A\u0627\u0631 ISO", ipv4: "\u0639\u0646\u0648\u0627\u0646 IPv4", ipv6: "\u0639\u0646\u0648\u0627\u0646 IPv6", cidrv4: "\u0645\u062F\u0649 \u0639\u0646\u0627\u0648\u064A\u0646 \u0628\u0635\u064A\u063A\u0629 IPv4", cidrv6: "\u0645\u062F\u0649 \u0639\u0646\u0627\u0648\u064A\u0646 \u0628\u0635\u064A\u063A\u0629 IPv6", base64: "\u0646\u064E\u0635 \u0628\u062A\u0631\u0645\u064A\u0632 base64-encoded", base64url: "\u0646\u064E\u0635 \u0628\u062A\u0631\u0645\u064A\u0632 base64url-encoded", json_string: "\u0646\u064E\u0635 \u0639\u0644\u0649 \u0647\u064A\u0626\u0629 JSON", e164: "\u0631\u0642\u0645 \u0647\u0627\u062A\u0641 \u0628\u0645\u0639\u064A\u0627\u0631 E.164", jwt: "JWT", template_literal: "\u0645\u062F\u062E\u0644" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0645\u062F\u062E\u0644\u0627\u062A \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644\u0629: \u064A\u0641\u062A\u0631\u0636 \u0625\u062F\u062E\u0627\u0644 ${Y.expected}\u060C \u0648\u0644\u0643\u0646 \u062A\u0645 \u0625\u062F\u062E\u0627\u0644 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0645\u062F\u062E\u0644\u0627\u062A \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644\u0629: \u064A\u0641\u062A\u0631\u0636 \u0625\u062F\u062E\u0627\u0644 ${S(Y.values[0])}`;
        return `\u0627\u062E\u062A\u064A\u0627\u0631 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062A\u0648\u0642\u0639 \u0627\u0646\u062A\u0642\u0627\u0621 \u0623\u062D\u062F \u0647\u0630\u0647 \u0627\u0644\u062E\u064A\u0627\u0631\u0627\u062A: ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return ` \u0623\u0643\u0628\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0623\u0646 \u062A\u0643\u0648\u0646 ${Y.origin ?? "\u0627\u0644\u0642\u064A\u0645\u0629"} ${z} ${Y.maximum.toString()} ${W.unit ?? "\u0639\u0646\u0635\u0631"}`;
        return `\u0623\u0643\u0628\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0623\u0646 \u062A\u0643\u0648\u0646 ${Y.origin ?? "\u0627\u0644\u0642\u064A\u0645\u0629"} ${z} ${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u0623\u0635\u063A\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0644\u0640 ${Y.origin} \u0623\u0646 \u064A\u0643\u0648\u0646 ${z} ${Y.minimum.toString()} ${W.unit}`;
        return `\u0623\u0635\u063A\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0644\u0640 ${Y.origin} \u0623\u0646 \u064A\u0643\u0648\u0646 ${z} ${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0628\u062F\u0623 \u0628\u0640 "${Y.prefix}"`;
        if (z.format === "ends_with") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0646\u062A\u0647\u064A \u0628\u0640 "${z.suffix}"`;
        if (z.format === "includes") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u062A\u0636\u0645\u0651\u064E\u0646 "${z.includes}"`;
        if (z.format === "regex") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0637\u0627\u0628\u0642 \u0627\u0644\u0646\u0645\u0637 ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644`;
      }
      case "not_multiple_of":
        return `\u0631\u0642\u0645 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0643\u0648\u0646 \u0645\u0646 \u0645\u0636\u0627\u0639\u0641\u0627\u062A ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u0645\u0639\u0631\u0641${Y.keys.length > 1 ? "\u0627\u062A" : ""} \u063A\u0631\u064A\u0628${Y.keys.length > 1 ? "\u0629" : ""}: ${M(Y.keys, "\u060C ")}`;
      case "invalid_key":
        return `\u0645\u0639\u0631\u0641 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644 \u0641\u064A ${Y.origin}`;
      case "invalid_union":
        return "\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644";
      case "invalid_element":
        return `\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644 \u0641\u064A ${Y.origin}`;
      default:
        return "\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644";
    }
  };
};
function wW() {
  return { localeError: BA() };
}
var qA = () => {
  let $ = { string: { unit: "simvol", verb: "olmal\u0131d\u0131r" }, file: { unit: "bayt", verb: "olmal\u0131d\u0131r" }, array: { unit: "element", verb: "olmal\u0131d\u0131r" }, set: { unit: "element", verb: "olmal\u0131d\u0131r" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "input", email: "email address", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datetime", date: "ISO date", time: "ISO time", duration: "ISO duration", ipv4: "IPv4 address", ipv6: "IPv6 address", cidrv4: "IPv4 range", cidrv6: "IPv6 range", base64: "base64-encoded string", base64url: "base64url-encoded string", json_string: "JSON string", e164: "E.164 number", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Yanl\u0131\u015F d\u0259y\u0259r: g\xF6zl\u0259nil\u0259n ${Y.expected}, daxil olan ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Yanl\u0131\u015F d\u0259y\u0259r: g\xF6zl\u0259nil\u0259n ${S(Y.values[0])}`;
        return `Yanl\u0131\u015F se\xE7im: a\u015Fa\u011F\u0131dak\u0131lardan biri olmal\u0131d\u0131r: ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\xC7ox b\xF6y\xFCk: g\xF6zl\u0259nil\u0259n ${Y.origin ?? "d\u0259y\u0259r"} ${z}${Y.maximum.toString()} ${W.unit ?? "element"}`;
        return `\xC7ox b\xF6y\xFCk: g\xF6zl\u0259nil\u0259n ${Y.origin ?? "d\u0259y\u0259r"} ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\xC7ox ki\xE7ik: g\xF6zl\u0259nil\u0259n ${Y.origin} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\xC7ox ki\xE7ik: g\xF6zl\u0259nil\u0259n ${Y.origin} ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Yanl\u0131\u015F m\u0259tn: "${z.prefix}" il\u0259 ba\u015Flamal\u0131d\u0131r`;
        if (z.format === "ends_with") return `Yanl\u0131\u015F m\u0259tn: "${z.suffix}" il\u0259 bitm\u0259lidir`;
        if (z.format === "includes") return `Yanl\u0131\u015F m\u0259tn: "${z.includes}" daxil olmal\u0131d\u0131r`;
        if (z.format === "regex") return `Yanl\u0131\u015F m\u0259tn: ${z.pattern} \u015Fablonuna uy\u011Fun olmal\u0131d\u0131r`;
        return `Yanl\u0131\u015F ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Yanl\u0131\u015F \u0259d\u0259d: ${Y.divisor} il\u0259 b\xF6l\xFCn\u0259 bil\u0259n olmal\u0131d\u0131r`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan a\xE7ar${Y.keys.length > 1 ? "lar" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `${Y.origin} daxilind\u0259 yanl\u0131\u015F a\xE7ar`;
      case "invalid_union":
        return "Yanl\u0131\u015F d\u0259y\u0259r";
      case "invalid_element":
        return `${Y.origin} daxilind\u0259 yanl\u0131\u015F d\u0259y\u0259r`;
      default:
        return "Yanl\u0131\u015F d\u0259y\u0259r";
    }
  };
};
function BW() {
  return { localeError: qA() };
}
function ZN($, X, J, Q) {
  let Y = Math.abs($), z = Y % 10, W = Y % 100;
  if (W >= 11 && W <= 19) return Q;
  if (z === 1) return X;
  if (z >= 2 && z <= 4) return J;
  return Q;
}
var DA = () => {
  let $ = { string: { unit: { one: "\u0441\u0456\u043C\u0432\u0430\u043B", few: "\u0441\u0456\u043C\u0432\u0430\u043B\u044B", many: "\u0441\u0456\u043C\u0432\u0430\u043B\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, array: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u044B", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, set: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u044B", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, file: { unit: { one: "\u0431\u0430\u0439\u0442", few: "\u0431\u0430\u0439\u0442\u044B", many: "\u0431\u0430\u0439\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u043B\u0456\u043A";
      case "object": {
        if (Array.isArray(Y)) return "\u043C\u0430\u0441\u0456\u045E";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0443\u0432\u043E\u0434", email: "email \u0430\u0434\u0440\u0430\u0441", url: "URL", emoji: "\u044D\u043C\u043E\u0434\u0437\u0456", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0430 \u0456 \u0447\u0430\u0441", date: "ISO \u0434\u0430\u0442\u0430", time: "ISO \u0447\u0430\u0441", duration: "ISO \u043F\u0440\u0430\u0446\u044F\u0433\u043B\u0430\u0441\u0446\u044C", ipv4: "IPv4 \u0430\u0434\u0440\u0430\u0441", ipv6: "IPv6 \u0430\u0434\u0440\u0430\u0441", cidrv4: "IPv4 \u0434\u044B\u044F\u043F\u0430\u0437\u043E\u043D", cidrv6: "IPv6 \u0434\u044B\u044F\u043F\u0430\u0437\u043E\u043D", base64: "\u0440\u0430\u0434\u043E\u043A \u0443 \u0444\u0430\u0440\u043C\u0430\u0446\u0435 base64", base64url: "\u0440\u0430\u0434\u043E\u043A \u0443 \u0444\u0430\u0440\u043C\u0430\u0446\u0435 base64url", json_string: "JSON \u0440\u0430\u0434\u043E\u043A", e164: "\u043D\u0443\u043C\u0430\u0440 E.164", jwt: "JWT", template_literal: "\u0443\u0432\u043E\u0434" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434: \u0447\u0430\u043A\u0430\u045E\u0441\u044F ${Y.expected}, \u0430\u0442\u0440\u044B\u043C\u0430\u043D\u0430 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F ${S(Y.values[0])}`;
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0432\u0430\u0440\u044B\u044F\u043D\u0442: \u0447\u0430\u043A\u0430\u045E\u0441\u044F \u0430\u0434\u0437\u0456\u043D \u0437 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) {
          let G = Number(Y.maximum), U = ZN(G, W.unit.one, W.unit.few, W.unit.many);
          return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u0432\u044F\u043B\u0456\u043A\u0456: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435"} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 ${W.verb} ${z}${Y.maximum.toString()} ${U}`;
        }
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u0432\u044F\u043B\u0456\u043A\u0456: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435"} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 \u0431\u044B\u0446\u044C ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) {
          let G = Number(Y.minimum), U = ZN(G, W.unit.one, W.unit.few, W.unit.many);
          return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u043C\u0430\u043B\u044B: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${Y.origin} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 ${W.verb} ${z}${Y.minimum.toString()} ${U}`;
        }
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u043C\u0430\u043B\u044B: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${Y.origin} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 \u0431\u044B\u0446\u044C ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u043F\u0430\u0447\u044B\u043D\u0430\u0446\u0446\u0430 \u0437 "${z.prefix}"`;
        if (z.format === "ends_with") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0437\u0430\u043A\u0430\u043D\u0447\u0432\u0430\u0446\u0446\u0430 \u043D\u0430 "${z.suffix}"`;
        if (z.format === "includes") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0437\u043C\u044F\u0448\u0447\u0430\u0446\u044C "${z.includes}"`;
        if (z.format === "regex") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0430\u0434\u043F\u0430\u0432\u044F\u0434\u0430\u0446\u044C \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${z.pattern}`;
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u043B\u0456\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0431\u044B\u0446\u044C \u043A\u0440\u0430\u0442\u043D\u044B\u043C ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u0430\u0441\u043F\u0430\u0437\u043D\u0430\u043D\u044B ${Y.keys.length > 1 ? "\u043A\u043B\u044E\u0447\u044B" : "\u043A\u043B\u044E\u0447"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u043A\u043B\u044E\u0447 \u0443 ${Y.origin}`;
      case "invalid_union":
        return "\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434";
      case "invalid_element":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u0430\u0435 \u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435 \u045E ${Y.origin}`;
      default:
        return "\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434";
    }
  };
};
function qW() {
  return { localeError: DA() };
}
var LA = () => {
  let $ = { string: { unit: "car\xE0cters", verb: "contenir" }, file: { unit: "bytes", verb: "contenir" }, array: { unit: "elements", verb: "contenir" }, set: { unit: "elements", verb: "contenir" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "entrada", email: "adre\xE7a electr\xF2nica", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data i hora ISO", date: "data ISO", time: "hora ISO", duration: "durada ISO", ipv4: "adre\xE7a IPv4", ipv6: "adre\xE7a IPv6", cidrv4: "rang IPv4", cidrv6: "rang IPv6", base64: "cadena codificada en base64", base64url: "cadena codificada en base64url", json_string: "cadena JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Tipus inv\xE0lid: s'esperava ${Y.expected}, s'ha rebut ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Valor inv\xE0lid: s'esperava ${S(Y.values[0])}`;
        return `Opci\xF3 inv\xE0lida: s'esperava una de ${M(Y.values, " o ")}`;
      case "too_big": {
        let z = Y.inclusive ? "com a m\xE0xim" : "menys de", W = X(Y.origin);
        if (W) return `Massa gran: s'esperava que ${Y.origin ?? "el valor"} contingu\xE9s ${z} ${Y.maximum.toString()} ${W.unit ?? "elements"}`;
        return `Massa gran: s'esperava que ${Y.origin ?? "el valor"} fos ${z} ${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? "com a m\xEDnim" : "m\xE9s de", W = X(Y.origin);
        if (W) return `Massa petit: s'esperava que ${Y.origin} contingu\xE9s ${z} ${Y.minimum.toString()} ${W.unit}`;
        return `Massa petit: s'esperava que ${Y.origin} fos ${z} ${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Format inv\xE0lid: ha de comen\xE7ar amb "${z.prefix}"`;
        if (z.format === "ends_with") return `Format inv\xE0lid: ha d'acabar amb "${z.suffix}"`;
        if (z.format === "includes") return `Format inv\xE0lid: ha d'incloure "${z.includes}"`;
        if (z.format === "regex") return `Format inv\xE0lid: ha de coincidir amb el patr\xF3 ${z.pattern}`;
        return `Format inv\xE0lid per a ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE0lid: ha de ser m\xFAltiple de ${Y.divisor}`;
      case "unrecognized_keys":
        return `Clau${Y.keys.length > 1 ? "s" : ""} no reconeguda${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Clau inv\xE0lida a ${Y.origin}`;
      case "invalid_union":
        return "Entrada inv\xE0lida";
      case "invalid_element":
        return `Element inv\xE0lid a ${Y.origin}`;
      default:
        return "Entrada inv\xE0lida";
    }
  };
};
function DW() {
  return { localeError: LA() };
}
var jA = () => {
  let $ = { string: { unit: "znak\u016F", verb: "m\xEDt" }, file: { unit: "bajt\u016F", verb: "m\xEDt" }, array: { unit: "prvk\u016F", verb: "m\xEDt" }, set: { unit: "prvk\u016F", verb: "m\xEDt" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u010D\xEDslo";
      case "string":
        return "\u0159et\u011Bzec";
      case "boolean":
        return "boolean";
      case "bigint":
        return "bigint";
      case "function":
        return "funkce";
      case "symbol":
        return "symbol";
      case "undefined":
        return "undefined";
      case "object": {
        if (Array.isArray(Y)) return "pole";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "regul\xE1rn\xED v\xFDraz", email: "e-mailov\xE1 adresa", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "datum a \u010Das ve form\xE1tu ISO", date: "datum ve form\xE1tu ISO", time: "\u010Das ve form\xE1tu ISO", duration: "doba trv\xE1n\xED ISO", ipv4: "IPv4 adresa", ipv6: "IPv6 adresa", cidrv4: "rozsah IPv4", cidrv6: "rozsah IPv6", base64: "\u0159et\u011Bzec zak\xF3dovan\xFD ve form\xE1tu base64", base64url: "\u0159et\u011Bzec zak\xF3dovan\xFD ve form\xE1tu base64url", json_string: "\u0159et\u011Bzec ve form\xE1tu JSON", e164: "\u010D\xEDslo E.164", jwt: "JWT", template_literal: "vstup" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Neplatn\xFD vstup: o\u010Dek\xE1v\xE1no ${Y.expected}, obdr\u017Eeno ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Neplatn\xFD vstup: o\u010Dek\xE1v\xE1no ${S(Y.values[0])}`;
        return `Neplatn\xE1 mo\u017Enost: o\u010Dek\xE1v\xE1na jedna z hodnot ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Hodnota je p\u0159\xEDli\u0161 velk\xE1: ${Y.origin ?? "hodnota"} mus\xED m\xEDt ${z}${Y.maximum.toString()} ${W.unit ?? "prvk\u016F"}`;
        return `Hodnota je p\u0159\xEDli\u0161 velk\xE1: ${Y.origin ?? "hodnota"} mus\xED b\xFDt ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Hodnota je p\u0159\xEDli\u0161 mal\xE1: ${Y.origin ?? "hodnota"} mus\xED m\xEDt ${z}${Y.minimum.toString()} ${W.unit ?? "prvk\u016F"}`;
        return `Hodnota je p\u0159\xEDli\u0161 mal\xE1: ${Y.origin ?? "hodnota"} mus\xED b\xFDt ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED za\u010D\xEDnat na "${z.prefix}"`;
        if (z.format === "ends_with") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED kon\u010Dit na "${z.suffix}"`;
        if (z.format === "includes") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED obsahovat "${z.includes}"`;
        if (z.format === "regex") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED odpov\xEDdat vzoru ${z.pattern}`;
        return `Neplatn\xFD form\xE1t ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Neplatn\xE9 \u010D\xEDslo: mus\xED b\xFDt n\xE1sobkem ${Y.divisor}`;
      case "unrecognized_keys":
        return `Nezn\xE1m\xE9 kl\xED\u010De: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Neplatn\xFD kl\xED\u010D v ${Y.origin}`;
      case "invalid_union":
        return "Neplatn\xFD vstup";
      case "invalid_element":
        return `Neplatn\xE1 hodnota v ${Y.origin}`;
      default:
        return "Neplatn\xFD vstup";
    }
  };
};
function LW() {
  return { localeError: jA() };
}
var FA = () => {
  let $ = { string: { unit: "Zeichen", verb: "zu haben" }, file: { unit: "Bytes", verb: "zu haben" }, array: { unit: "Elemente", verb: "zu haben" }, set: { unit: "Elemente", verb: "zu haben" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "Zahl";
      case "object": {
        if (Array.isArray(Y)) return "Array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "Eingabe", email: "E-Mail-Adresse", url: "URL", emoji: "Emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-Datum und -Uhrzeit", date: "ISO-Datum", time: "ISO-Uhrzeit", duration: "ISO-Dauer", ipv4: "IPv4-Adresse", ipv6: "IPv6-Adresse", cidrv4: "IPv4-Bereich", cidrv6: "IPv6-Bereich", base64: "Base64-codierter String", base64url: "Base64-URL-codierter String", json_string: "JSON-String", e164: "E.164-Nummer", jwt: "JWT", template_literal: "Eingabe" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Ung\xFCltige Eingabe: erwartet ${Y.expected}, erhalten ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Ung\xFCltige Eingabe: erwartet ${S(Y.values[0])}`;
        return `Ung\xFCltige Option: erwartet eine von ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Zu gro\xDF: erwartet, dass ${Y.origin ?? "Wert"} ${z}${Y.maximum.toString()} ${W.unit ?? "Elemente"} hat`;
        return `Zu gro\xDF: erwartet, dass ${Y.origin ?? "Wert"} ${z}${Y.maximum.toString()} ist`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Zu klein: erwartet, dass ${Y.origin} ${z}${Y.minimum.toString()} ${W.unit} hat`;
        return `Zu klein: erwartet, dass ${Y.origin} ${z}${Y.minimum.toString()} ist`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Ung\xFCltiger String: muss mit "${z.prefix}" beginnen`;
        if (z.format === "ends_with") return `Ung\xFCltiger String: muss mit "${z.suffix}" enden`;
        if (z.format === "includes") return `Ung\xFCltiger String: muss "${z.includes}" enthalten`;
        if (z.format === "regex") return `Ung\xFCltiger String: muss dem Muster ${z.pattern} entsprechen`;
        return `Ung\xFCltig: ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Ung\xFCltige Zahl: muss ein Vielfaches von ${Y.divisor} sein`;
      case "unrecognized_keys":
        return `${Y.keys.length > 1 ? "Unbekannte Schl\xFCssel" : "Unbekannter Schl\xFCssel"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Ung\xFCltiger Schl\xFCssel in ${Y.origin}`;
      case "invalid_union":
        return "Ung\xFCltige Eingabe";
      case "invalid_element":
        return `Ung\xFCltiger Wert in ${Y.origin}`;
      default:
        return "Ung\xFCltige Eingabe";
    }
  };
};
function jW() {
  return { localeError: FA() };
}
var MA = ($) => {
  let X = typeof $;
  switch (X) {
    case "number":
      return Number.isNaN($) ? "NaN" : "number";
    case "object": {
      if (Array.isArray($)) return "array";
      if ($ === null) return "null";
      if (Object.getPrototypeOf($) !== Object.prototype && $.constructor) return $.constructor.name;
    }
  }
  return X;
};
var IA = () => {
  let $ = { string: { unit: "characters", verb: "to have" }, file: { unit: "bytes", verb: "to have" }, array: { unit: "items", verb: "to have" }, set: { unit: "items", verb: "to have" } };
  function X(Q) {
    return $[Q] ?? null;
  }
  let J = { regex: "input", email: "email address", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datetime", date: "ISO date", time: "ISO time", duration: "ISO duration", ipv4: "IPv4 address", ipv6: "IPv6 address", cidrv4: "IPv4 range", cidrv6: "IPv6 range", base64: "base64-encoded string", base64url: "base64url-encoded string", json_string: "JSON string", e164: "E.164 number", jwt: "JWT", template_literal: "input" };
  return (Q) => {
    switch (Q.code) {
      case "invalid_type":
        return `Invalid input: expected ${Q.expected}, received ${MA(Q.input)}`;
      case "invalid_value":
        if (Q.values.length === 1) return `Invalid input: expected ${S(Q.values[0])}`;
        return `Invalid option: expected one of ${M(Q.values, "|")}`;
      case "too_big": {
        let Y = Q.inclusive ? "<=" : "<", z = X(Q.origin);
        if (z) return `Too big: expected ${Q.origin ?? "value"} to have ${Y}${Q.maximum.toString()} ${z.unit ?? "elements"}`;
        return `Too big: expected ${Q.origin ?? "value"} to be ${Y}${Q.maximum.toString()}`;
      }
      case "too_small": {
        let Y = Q.inclusive ? ">=" : ">", z = X(Q.origin);
        if (z) return `Too small: expected ${Q.origin} to have ${Y}${Q.minimum.toString()} ${z.unit}`;
        return `Too small: expected ${Q.origin} to be ${Y}${Q.minimum.toString()}`;
      }
      case "invalid_format": {
        let Y = Q;
        if (Y.format === "starts_with") return `Invalid string: must start with "${Y.prefix}"`;
        if (Y.format === "ends_with") return `Invalid string: must end with "${Y.suffix}"`;
        if (Y.format === "includes") return `Invalid string: must include "${Y.includes}"`;
        if (Y.format === "regex") return `Invalid string: must match pattern ${Y.pattern}`;
        return `Invalid ${J[Y.format] ?? Q.format}`;
      }
      case "not_multiple_of":
        return `Invalid number: must be a multiple of ${Q.divisor}`;
      case "unrecognized_keys":
        return `Unrecognized key${Q.keys.length > 1 ? "s" : ""}: ${M(Q.keys, ", ")}`;
      case "invalid_key":
        return `Invalid key in ${Q.origin}`;
      case "invalid_union":
        return "Invalid input";
      case "invalid_element":
        return `Invalid value in ${Q.origin}`;
      default:
        return "Invalid input";
    }
  };
};
function M8() {
  return { localeError: IA() };
}
var AA = ($) => {
  let X = typeof $;
  switch (X) {
    case "number":
      return Number.isNaN($) ? "NaN" : "nombro";
    case "object": {
      if (Array.isArray($)) return "tabelo";
      if ($ === null) return "senvalora";
      if (Object.getPrototypeOf($) !== Object.prototype && $.constructor) return $.constructor.name;
    }
  }
  return X;
};
var bA = () => {
  let $ = { string: { unit: "karaktrojn", verb: "havi" }, file: { unit: "bajtojn", verb: "havi" }, array: { unit: "elementojn", verb: "havi" }, set: { unit: "elementojn", verb: "havi" } };
  function X(Q) {
    return $[Q] ?? null;
  }
  let J = { regex: "enigo", email: "retadreso", url: "URL", emoji: "emo\u011Dio", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-datotempo", date: "ISO-dato", time: "ISO-tempo", duration: "ISO-da\u016Dro", ipv4: "IPv4-adreso", ipv6: "IPv6-adreso", cidrv4: "IPv4-rango", cidrv6: "IPv6-rango", base64: "64-ume kodita karaktraro", base64url: "URL-64-ume kodita karaktraro", json_string: "JSON-karaktraro", e164: "E.164-nombro", jwt: "JWT", template_literal: "enigo" };
  return (Q) => {
    switch (Q.code) {
      case "invalid_type":
        return `Nevalida enigo: atendi\u011Dis ${Q.expected}, ricevi\u011Dis ${AA(Q.input)}`;
      case "invalid_value":
        if (Q.values.length === 1) return `Nevalida enigo: atendi\u011Dis ${S(Q.values[0])}`;
        return `Nevalida opcio: atendi\u011Dis unu el ${M(Q.values, "|")}`;
      case "too_big": {
        let Y = Q.inclusive ? "<=" : "<", z = X(Q.origin);
        if (z) return `Tro granda: atendi\u011Dis ke ${Q.origin ?? "valoro"} havu ${Y}${Q.maximum.toString()} ${z.unit ?? "elementojn"}`;
        return `Tro granda: atendi\u011Dis ke ${Q.origin ?? "valoro"} havu ${Y}${Q.maximum.toString()}`;
      }
      case "too_small": {
        let Y = Q.inclusive ? ">=" : ">", z = X(Q.origin);
        if (z) return `Tro malgranda: atendi\u011Dis ke ${Q.origin} havu ${Y}${Q.minimum.toString()} ${z.unit}`;
        return `Tro malgranda: atendi\u011Dis ke ${Q.origin} estu ${Y}${Q.minimum.toString()}`;
      }
      case "invalid_format": {
        let Y = Q;
        if (Y.format === "starts_with") return `Nevalida karaktraro: devas komenci\u011Di per "${Y.prefix}"`;
        if (Y.format === "ends_with") return `Nevalida karaktraro: devas fini\u011Di per "${Y.suffix}"`;
        if (Y.format === "includes") return `Nevalida karaktraro: devas inkluzivi "${Y.includes}"`;
        if (Y.format === "regex") return `Nevalida karaktraro: devas kongrui kun la modelo ${Y.pattern}`;
        return `Nevalida ${J[Y.format] ?? Q.format}`;
      }
      case "not_multiple_of":
        return `Nevalida nombro: devas esti oblo de ${Q.divisor}`;
      case "unrecognized_keys":
        return `Nekonata${Q.keys.length > 1 ? "j" : ""} \u015Dlosilo${Q.keys.length > 1 ? "j" : ""}: ${M(Q.keys, ", ")}`;
      case "invalid_key":
        return `Nevalida \u015Dlosilo en ${Q.origin}`;
      case "invalid_union":
        return "Nevalida enigo";
      case "invalid_element":
        return `Nevalida valoro en ${Q.origin}`;
      default:
        return "Nevalida enigo";
    }
  };
};
function FW() {
  return { localeError: bA() };
}
var PA = () => {
  let $ = { string: { unit: "caracteres", verb: "tener" }, file: { unit: "bytes", verb: "tener" }, array: { unit: "elementos", verb: "tener" }, set: { unit: "elementos", verb: "tener" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "n\xFAmero";
      case "object": {
        if (Array.isArray(Y)) return "arreglo";
        if (Y === null) return "nulo";
        if (Object.getPrototypeOf(Y) !== Object.prototype) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "entrada", email: "direcci\xF3n de correo electr\xF3nico", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "fecha y hora ISO", date: "fecha ISO", time: "hora ISO", duration: "duraci\xF3n ISO", ipv4: "direcci\xF3n IPv4", ipv6: "direcci\xF3n IPv6", cidrv4: "rango IPv4", cidrv6: "rango IPv6", base64: "cadena codificada en base64", base64url: "URL codificada en base64", json_string: "cadena JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Entrada inv\xE1lida: se esperaba ${Y.expected}, recibido ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Entrada inv\xE1lida: se esperaba ${S(Y.values[0])}`;
        return `Opci\xF3n inv\xE1lida: se esperaba una de ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Demasiado grande: se esperaba que ${Y.origin ?? "valor"} tuviera ${z}${Y.maximum.toString()} ${W.unit ?? "elementos"}`;
        return `Demasiado grande: se esperaba que ${Y.origin ?? "valor"} fuera ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Demasiado peque\xF1o: se esperaba que ${Y.origin} tuviera ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Demasiado peque\xF1o: se esperaba que ${Y.origin} fuera ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Cadena inv\xE1lida: debe comenzar con "${z.prefix}"`;
        if (z.format === "ends_with") return `Cadena inv\xE1lida: debe terminar en "${z.suffix}"`;
        if (z.format === "includes") return `Cadena inv\xE1lida: debe incluir "${z.includes}"`;
        if (z.format === "regex") return `Cadena inv\xE1lida: debe coincidir con el patr\xF3n ${z.pattern}`;
        return `Inv\xE1lido ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE1lido: debe ser m\xFAltiplo de ${Y.divisor}`;
      case "unrecognized_keys":
        return `Llave${Y.keys.length > 1 ? "s" : ""} desconocida${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Llave inv\xE1lida en ${Y.origin}`;
      case "invalid_union":
        return "Entrada inv\xE1lida";
      case "invalid_element":
        return `Valor inv\xE1lido en ${Y.origin}`;
      default:
        return "Entrada inv\xE1lida";
    }
  };
};
function MW() {
  return { localeError: PA() };
}
var ZA = () => {
  let $ = { string: { unit: "\u06A9\u0627\u0631\u0627\u06A9\u062A\u0631", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, file: { unit: "\u0628\u0627\u06CC\u062A", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, array: { unit: "\u0622\u06CC\u062A\u0645", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, set: { unit: "\u0622\u06CC\u062A\u0645", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0639\u062F\u062F";
      case "object": {
        if (Array.isArray(Y)) return "\u0622\u0631\u0627\u06CC\u0647";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0648\u0631\u0648\u062F\u06CC", email: "\u0622\u062F\u0631\u0633 \u0627\u06CC\u0645\u06CC\u0644", url: "URL", emoji: "\u0627\u06CC\u0645\u0648\u062C\u06CC", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u062A\u0627\u0631\u06CC\u062E \u0648 \u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", date: "\u062A\u0627\u0631\u06CC\u062E \u0627\u06CC\u0632\u0648", time: "\u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", duration: "\u0645\u062F\u062A \u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", ipv4: "IPv4 \u0622\u062F\u0631\u0633", ipv6: "IPv6 \u0622\u062F\u0631\u0633", cidrv4: "IPv4 \u062F\u0627\u0645\u0646\u0647", cidrv6: "IPv6 \u062F\u0627\u0645\u0646\u0647", base64: "base64-encoded \u0631\u0634\u062A\u0647", base64url: "base64url-encoded \u0631\u0634\u062A\u0647", json_string: "JSON \u0631\u0634\u062A\u0647", e164: "E.164 \u0639\u062F\u062F", jwt: "JWT", template_literal: "\u0648\u0631\u0648\u062F\u06CC" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A ${Y.expected} \u0645\u06CC\u200C\u0628\u0648\u062F\u060C ${J(Y.input)} \u062F\u0631\u06CC\u0627\u0641\u062A \u0634\u062F`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A ${S(Y.values[0])} \u0645\u06CC\u200C\u0628\u0648\u062F`;
        return `\u06AF\u0632\u06CC\u0646\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A \u06CC\u06A9\u06CC \u0627\u0632 ${M(Y.values, "|")} \u0645\u06CC\u200C\u0628\u0648\u062F`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u062E\u06CC\u0644\u06CC \u0628\u0632\u0631\u06AF: ${Y.origin ?? "\u0645\u0642\u062F\u0627\u0631"} \u0628\u0627\u06CC\u062F ${z}${Y.maximum.toString()} ${W.unit ?? "\u0639\u0646\u0635\u0631"} \u0628\u0627\u0634\u062F`;
        return `\u062E\u06CC\u0644\u06CC \u0628\u0632\u0631\u06AF: ${Y.origin ?? "\u0645\u0642\u062F\u0627\u0631"} \u0628\u0627\u06CC\u062F ${z}${Y.maximum.toString()} \u0628\u0627\u0634\u062F`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u062E\u06CC\u0644\u06CC \u06A9\u0648\u0686\u06A9: ${Y.origin} \u0628\u0627\u06CC\u062F ${z}${Y.minimum.toString()} ${W.unit} \u0628\u0627\u0634\u062F`;
        return `\u062E\u06CC\u0644\u06CC \u06A9\u0648\u0686\u06A9: ${Y.origin} \u0628\u0627\u06CC\u062F ${z}${Y.minimum.toString()} \u0628\u0627\u0634\u062F`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 "${z.prefix}" \u0634\u0631\u0648\u0639 \u0634\u0648\u062F`;
        if (z.format === "ends_with") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 "${z.suffix}" \u062A\u0645\u0627\u0645 \u0634\u0648\u062F`;
        if (z.format === "includes") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0634\u0627\u0645\u0644 "${z.includes}" \u0628\u0627\u0634\u062F`;
        if (z.format === "regex") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 \u0627\u0644\u06AF\u0648\u06CC ${z.pattern} \u0645\u0637\u0627\u0628\u0642\u062A \u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F`;
        return `${Q[z.format] ?? Y.format} \u0646\u0627\u0645\u0639\u062A\u0628\u0631`;
      }
      case "not_multiple_of":
        return `\u0639\u062F\u062F \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0645\u0636\u0631\u0628 ${Y.divisor} \u0628\u0627\u0634\u062F`;
      case "unrecognized_keys":
        return `\u06A9\u0644\u06CC\u062F${Y.keys.length > 1 ? "\u0647\u0627\u06CC" : ""} \u0646\u0627\u0634\u0646\u0627\u0633: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u06A9\u0644\u06CC\u062F \u0646\u0627\u0634\u0646\u0627\u0633 \u062F\u0631 ${Y.origin}`;
      case "invalid_union":
        return "\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631";
      case "invalid_element":
        return `\u0645\u0642\u062F\u0627\u0631 \u0646\u0627\u0645\u0639\u062A\u0628\u0631 \u062F\u0631 ${Y.origin}`;
      default:
        return "\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631";
    }
  };
};
function IW() {
  return { localeError: ZA() };
}
var EA = () => {
  let $ = { string: { unit: "merkki\xE4", subject: "merkkijonon" }, file: { unit: "tavua", subject: "tiedoston" }, array: { unit: "alkiota", subject: "listan" }, set: { unit: "alkiota", subject: "joukon" }, number: { unit: "", subject: "luvun" }, bigint: { unit: "", subject: "suuren kokonaisluvun" }, int: { unit: "", subject: "kokonaisluvun" }, date: { unit: "", subject: "p\xE4iv\xE4m\xE4\xE4r\xE4n" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "s\xE4\xE4nn\xF6llinen lauseke", email: "s\xE4hk\xF6postiosoite", url: "URL-osoite", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-aikaleima", date: "ISO-p\xE4iv\xE4m\xE4\xE4r\xE4", time: "ISO-aika", duration: "ISO-kesto", ipv4: "IPv4-osoite", ipv6: "IPv6-osoite", cidrv4: "IPv4-alue", cidrv6: "IPv6-alue", base64: "base64-koodattu merkkijono", base64url: "base64url-koodattu merkkijono", json_string: "JSON-merkkijono", e164: "E.164-luku", jwt: "JWT", template_literal: "templaattimerkkijono" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Virheellinen tyyppi: odotettiin ${Y.expected}, oli ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Virheellinen sy\xF6te: t\xE4ytyy olla ${S(Y.values[0])}`;
        return `Virheellinen valinta: t\xE4ytyy olla yksi seuraavista: ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Liian suuri: ${W.subject} t\xE4ytyy olla ${z}${Y.maximum.toString()} ${W.unit}`.trim();
        return `Liian suuri: arvon t\xE4ytyy olla ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Liian pieni: ${W.subject} t\xE4ytyy olla ${z}${Y.minimum.toString()} ${W.unit}`.trim();
        return `Liian pieni: arvon t\xE4ytyy olla ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Virheellinen sy\xF6te: t\xE4ytyy alkaa "${z.prefix}"`;
        if (z.format === "ends_with") return `Virheellinen sy\xF6te: t\xE4ytyy loppua "${z.suffix}"`;
        if (z.format === "includes") return `Virheellinen sy\xF6te: t\xE4ytyy sis\xE4lt\xE4\xE4 "${z.includes}"`;
        if (z.format === "regex") return `Virheellinen sy\xF6te: t\xE4ytyy vastata s\xE4\xE4nn\xF6llist\xE4 lauseketta ${z.pattern}`;
        return `Virheellinen ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Virheellinen luku: t\xE4ytyy olla luvun ${Y.divisor} monikerta`;
      case "unrecognized_keys":
        return `${Y.keys.length > 1 ? "Tuntemattomat avaimet" : "Tuntematon avain"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return "Virheellinen avain tietueessa";
      case "invalid_union":
        return "Virheellinen unioni";
      case "invalid_element":
        return "Virheellinen arvo joukossa";
      default:
        return "Virheellinen sy\xF6te";
    }
  };
};
function AW() {
  return { localeError: EA() };
}
var RA = () => {
  let $ = { string: { unit: "caract\xE8res", verb: "avoir" }, file: { unit: "octets", verb: "avoir" }, array: { unit: "\xE9l\xE9ments", verb: "avoir" }, set: { unit: "\xE9l\xE9ments", verb: "avoir" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "nombre";
      case "object": {
        if (Array.isArray(Y)) return "tableau";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "entr\xE9e", email: "adresse e-mail", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "date et heure ISO", date: "date ISO", time: "heure ISO", duration: "dur\xE9e ISO", ipv4: "adresse IPv4", ipv6: "adresse IPv6", cidrv4: "plage IPv4", cidrv6: "plage IPv6", base64: "cha\xEEne encod\xE9e en base64", base64url: "cha\xEEne encod\xE9e en base64url", json_string: "cha\xEEne JSON", e164: "num\xE9ro E.164", jwt: "JWT", template_literal: "entr\xE9e" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Entr\xE9e invalide : ${Y.expected} attendu, ${J(Y.input)} re\xE7u`;
      case "invalid_value":
        if (Y.values.length === 1) return `Entr\xE9e invalide : ${S(Y.values[0])} attendu`;
        return `Option invalide : une valeur parmi ${M(Y.values, "|")} attendue`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Trop grand : ${Y.origin ?? "valeur"} doit ${W.verb} ${z}${Y.maximum.toString()} ${W.unit ?? "\xE9l\xE9ment(s)"}`;
        return `Trop grand : ${Y.origin ?? "valeur"} doit \xEAtre ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Trop petit : ${Y.origin} doit ${W.verb} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Trop petit : ${Y.origin} doit \xEAtre ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Cha\xEEne invalide : doit commencer par "${z.prefix}"`;
        if (z.format === "ends_with") return `Cha\xEEne invalide : doit se terminer par "${z.suffix}"`;
        if (z.format === "includes") return `Cha\xEEne invalide : doit inclure "${z.includes}"`;
        if (z.format === "regex") return `Cha\xEEne invalide : doit correspondre au mod\xE8le ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} invalide`;
      }
      case "not_multiple_of":
        return `Nombre invalide : doit \xEAtre un multiple de ${Y.divisor}`;
      case "unrecognized_keys":
        return `Cl\xE9${Y.keys.length > 1 ? "s" : ""} non reconnue${Y.keys.length > 1 ? "s" : ""} : ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Cl\xE9 invalide dans ${Y.origin}`;
      case "invalid_union":
        return "Entr\xE9e invalide";
      case "invalid_element":
        return `Valeur invalide dans ${Y.origin}`;
      default:
        return "Entr\xE9e invalide";
    }
  };
};
function bW() {
  return { localeError: RA() };
}
var SA = () => {
  let $ = { string: { unit: "caract\xE8res", verb: "avoir" }, file: { unit: "octets", verb: "avoir" }, array: { unit: "\xE9l\xE9ments", verb: "avoir" }, set: { unit: "\xE9l\xE9ments", verb: "avoir" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "entr\xE9e", email: "adresse courriel", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "date-heure ISO", date: "date ISO", time: "heure ISO", duration: "dur\xE9e ISO", ipv4: "adresse IPv4", ipv6: "adresse IPv6", cidrv4: "plage IPv4", cidrv6: "plage IPv6", base64: "cha\xEEne encod\xE9e en base64", base64url: "cha\xEEne encod\xE9e en base64url", json_string: "cha\xEEne JSON", e164: "num\xE9ro E.164", jwt: "JWT", template_literal: "entr\xE9e" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Entr\xE9e invalide : attendu ${Y.expected}, re\xE7u ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Entr\xE9e invalide : attendu ${S(Y.values[0])}`;
        return `Option invalide : attendu l'une des valeurs suivantes ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "\u2264" : "<", W = X(Y.origin);
        if (W) return `Trop grand : attendu que ${Y.origin ?? "la valeur"} ait ${z}${Y.maximum.toString()} ${W.unit}`;
        return `Trop grand : attendu que ${Y.origin ?? "la valeur"} soit ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? "\u2265" : ">", W = X(Y.origin);
        if (W) return `Trop petit : attendu que ${Y.origin} ait ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Trop petit : attendu que ${Y.origin} soit ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Cha\xEEne invalide : doit commencer par "${z.prefix}"`;
        if (z.format === "ends_with") return `Cha\xEEne invalide : doit se terminer par "${z.suffix}"`;
        if (z.format === "includes") return `Cha\xEEne invalide : doit inclure "${z.includes}"`;
        if (z.format === "regex") return `Cha\xEEne invalide : doit correspondre au motif ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} invalide`;
      }
      case "not_multiple_of":
        return `Nombre invalide : doit \xEAtre un multiple de ${Y.divisor}`;
      case "unrecognized_keys":
        return `Cl\xE9${Y.keys.length > 1 ? "s" : ""} non reconnue${Y.keys.length > 1 ? "s" : ""} : ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Cl\xE9 invalide dans ${Y.origin}`;
      case "invalid_union":
        return "Entr\xE9e invalide";
      case "invalid_element":
        return `Valeur invalide dans ${Y.origin}`;
      default:
        return "Entr\xE9e invalide";
    }
  };
};
function PW() {
  return { localeError: SA() };
}
var vA = () => {
  let $ = { string: { unit: "\u05D0\u05D5\u05EA\u05D9\u05D5\u05EA", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, file: { unit: "\u05D1\u05D9\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, array: { unit: "\u05E4\u05E8\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, set: { unit: "\u05E4\u05E8\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u05E7\u05DC\u05D8", email: "\u05DB\u05EA\u05D5\u05D1\u05EA \u05D0\u05D9\u05DE\u05D9\u05D9\u05DC", url: "\u05DB\u05EA\u05D5\u05D1\u05EA \u05E8\u05E9\u05EA", emoji: "\u05D0\u05D9\u05DE\u05D5\u05D2'\u05D9", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u05EA\u05D0\u05E8\u05D9\u05DA \u05D5\u05D6\u05DE\u05DF ISO", date: "\u05EA\u05D0\u05E8\u05D9\u05DA ISO", time: "\u05D6\u05DE\u05DF ISO", duration: "\u05DE\u05E9\u05DA \u05D6\u05DE\u05DF ISO", ipv4: "\u05DB\u05EA\u05D5\u05D1\u05EA IPv4", ipv6: "\u05DB\u05EA\u05D5\u05D1\u05EA IPv6", cidrv4: "\u05D8\u05D5\u05D5\u05D7 IPv4", cidrv6: "\u05D8\u05D5\u05D5\u05D7 IPv6", base64: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05D1\u05D1\u05E1\u05D9\u05E1 64", base64url: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05D1\u05D1\u05E1\u05D9\u05E1 64 \u05DC\u05DB\u05EA\u05D5\u05D1\u05D5\u05EA \u05E8\u05E9\u05EA", json_string: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA JSON", e164: "\u05DE\u05E1\u05E4\u05E8 E.164", jwt: "JWT", template_literal: "\u05E7\u05DC\u05D8" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA ${Y.expected}, \u05D4\u05EA\u05E7\u05D1\u05DC ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA ${S(Y.values[0])}`;
        return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA \u05D0\u05D7\u05EA \u05DE\u05D4\u05D0\u05E4\u05E9\u05E8\u05D5\u05D9\u05D5\u05EA  ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u05D2\u05D3\u05D5\u05DC \u05DE\u05D3\u05D9: ${Y.origin ?? "value"} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${z}${Y.maximum.toString()} ${W.unit ?? "elements"}`;
        return `\u05D2\u05D3\u05D5\u05DC \u05DE\u05D3\u05D9: ${Y.origin ?? "value"} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u05E7\u05D8\u05DF \u05DE\u05D3\u05D9: ${Y.origin} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\u05E7\u05D8\u05DF \u05DE\u05D3\u05D9: ${Y.origin} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05EA\u05D7\u05D9\u05DC \u05D1"${z.prefix}"`;
        if (z.format === "ends_with") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05E1\u05EA\u05D9\u05D9\u05DD \u05D1 "${z.suffix}"`;
        if (z.format === "includes") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05DB\u05DC\u05D5\u05DC "${z.includes}"`;
        if (z.format === "regex") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05EA\u05D0\u05D9\u05DD \u05DC\u05EA\u05D1\u05E0\u05D9\u05EA ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF`;
      }
      case "not_multiple_of":
        return `\u05DE\u05E1\u05E4\u05E8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05D7\u05D9\u05D9\u05D1 \u05DC\u05D4\u05D9\u05D5\u05EA \u05DE\u05DB\u05E4\u05DC\u05D4 \u05E9\u05DC ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u05DE\u05E4\u05EA\u05D7${Y.keys.length > 1 ? "\u05D5\u05EA" : ""} \u05DC\u05D0 \u05DE\u05D6\u05D5\u05D4${Y.keys.length > 1 ? "\u05D9\u05DD" : "\u05D4"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u05DE\u05E4\u05EA\u05D7 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF \u05D1${Y.origin}`;
      case "invalid_union":
        return "\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF";
      case "invalid_element":
        return `\u05E2\u05E8\u05DA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF \u05D1${Y.origin}`;
      default:
        return "\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF";
    }
  };
};
function ZW() {
  return { localeError: vA() };
}
var CA = () => {
  let $ = { string: { unit: "karakter", verb: "legyen" }, file: { unit: "byte", verb: "legyen" }, array: { unit: "elem", verb: "legyen" }, set: { unit: "elem", verb: "legyen" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "sz\xE1m";
      case "object": {
        if (Array.isArray(Y)) return "t\xF6mb";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "bemenet", email: "email c\xEDm", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO id\u0151b\xE9lyeg", date: "ISO d\xE1tum", time: "ISO id\u0151", duration: "ISO id\u0151intervallum", ipv4: "IPv4 c\xEDm", ipv6: "IPv6 c\xEDm", cidrv4: "IPv4 tartom\xE1ny", cidrv6: "IPv6 tartom\xE1ny", base64: "base64-k\xF3dolt string", base64url: "base64url-k\xF3dolt string", json_string: "JSON string", e164: "E.164 sz\xE1m", jwt: "JWT", template_literal: "bemenet" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\xC9rv\xE9nytelen bemenet: a v\xE1rt \xE9rt\xE9k ${Y.expected}, a kapott \xE9rt\xE9k ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\xC9rv\xE9nytelen bemenet: a v\xE1rt \xE9rt\xE9k ${S(Y.values[0])}`;
        return `\xC9rv\xE9nytelen opci\xF3: valamelyik \xE9rt\xE9k v\xE1rt ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `T\xFAl nagy: ${Y.origin ?? "\xE9rt\xE9k"} m\xE9rete t\xFAl nagy ${z}${Y.maximum.toString()} ${W.unit ?? "elem"}`;
        return `T\xFAl nagy: a bemeneti \xE9rt\xE9k ${Y.origin ?? "\xE9rt\xE9k"} t\xFAl nagy: ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `T\xFAl kicsi: a bemeneti \xE9rt\xE9k ${Y.origin} m\xE9rete t\xFAl kicsi ${z}${Y.minimum.toString()} ${W.unit}`;
        return `T\xFAl kicsi: a bemeneti \xE9rt\xE9k ${Y.origin} t\xFAl kicsi ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\xC9rv\xE9nytelen string: "${z.prefix}" \xE9rt\xE9kkel kell kezd\u0151dnie`;
        if (z.format === "ends_with") return `\xC9rv\xE9nytelen string: "${z.suffix}" \xE9rt\xE9kkel kell v\xE9gz\u0151dnie`;
        if (z.format === "includes") return `\xC9rv\xE9nytelen string: "${z.includes}" \xE9rt\xE9ket kell tartalmaznia`;
        if (z.format === "regex") return `\xC9rv\xE9nytelen string: ${z.pattern} mint\xE1nak kell megfelelnie`;
        return `\xC9rv\xE9nytelen ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\xC9rv\xE9nytelen sz\xE1m: ${Y.divisor} t\xF6bbsz\xF6r\xF6s\xE9nek kell lennie`;
      case "unrecognized_keys":
        return `Ismeretlen kulcs${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\xC9rv\xE9nytelen kulcs ${Y.origin}`;
      case "invalid_union":
        return "\xC9rv\xE9nytelen bemenet";
      case "invalid_element":
        return `\xC9rv\xE9nytelen \xE9rt\xE9k: ${Y.origin}`;
      default:
        return "\xC9rv\xE9nytelen bemenet";
    }
  };
};
function EW() {
  return { localeError: CA() };
}
var kA = () => {
  let $ = { string: { unit: "karakter", verb: "memiliki" }, file: { unit: "byte", verb: "memiliki" }, array: { unit: "item", verb: "memiliki" }, set: { unit: "item", verb: "memiliki" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "input", email: "alamat email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "tanggal dan waktu format ISO", date: "tanggal format ISO", time: "jam format ISO", duration: "durasi format ISO", ipv4: "alamat IPv4", ipv6: "alamat IPv6", cidrv4: "rentang alamat IPv4", cidrv6: "rentang alamat IPv6", base64: "string dengan enkode base64", base64url: "string dengan enkode base64url", json_string: "string JSON", e164: "angka E.164", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Input tidak valid: diharapkan ${Y.expected}, diterima ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Input tidak valid: diharapkan ${S(Y.values[0])}`;
        return `Pilihan tidak valid: diharapkan salah satu dari ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Terlalu besar: diharapkan ${Y.origin ?? "value"} memiliki ${z}${Y.maximum.toString()} ${W.unit ?? "elemen"}`;
        return `Terlalu besar: diharapkan ${Y.origin ?? "value"} menjadi ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Terlalu kecil: diharapkan ${Y.origin} memiliki ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Terlalu kecil: diharapkan ${Y.origin} menjadi ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `String tidak valid: harus dimulai dengan "${z.prefix}"`;
        if (z.format === "ends_with") return `String tidak valid: harus berakhir dengan "${z.suffix}"`;
        if (z.format === "includes") return `String tidak valid: harus menyertakan "${z.includes}"`;
        if (z.format === "regex") return `String tidak valid: harus sesuai pola ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} tidak valid`;
      }
      case "not_multiple_of":
        return `Angka tidak valid: harus kelipatan dari ${Y.divisor}`;
      case "unrecognized_keys":
        return `Kunci tidak dikenali ${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Kunci tidak valid di ${Y.origin}`;
      case "invalid_union":
        return "Input tidak valid";
      case "invalid_element":
        return `Nilai tidak valid di ${Y.origin}`;
      default:
        return "Input tidak valid";
    }
  };
};
function RW() {
  return { localeError: kA() };
}
var _A = () => {
  let $ = { string: { unit: "caratteri", verb: "avere" }, file: { unit: "byte", verb: "avere" }, array: { unit: "elementi", verb: "avere" }, set: { unit: "elementi", verb: "avere" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "numero";
      case "object": {
        if (Array.isArray(Y)) return "vettore";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "input", email: "indirizzo email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data e ora ISO", date: "data ISO", time: "ora ISO", duration: "durata ISO", ipv4: "indirizzo IPv4", ipv6: "indirizzo IPv6", cidrv4: "intervallo IPv4", cidrv6: "intervallo IPv6", base64: "stringa codificata in base64", base64url: "URL codificata in base64", json_string: "stringa JSON", e164: "numero E.164", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Input non valido: atteso ${Y.expected}, ricevuto ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Input non valido: atteso ${S(Y.values[0])}`;
        return `Opzione non valida: atteso uno tra ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Troppo grande: ${Y.origin ?? "valore"} deve avere ${z}${Y.maximum.toString()} ${W.unit ?? "elementi"}`;
        return `Troppo grande: ${Y.origin ?? "valore"} deve essere ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Troppo piccolo: ${Y.origin} deve avere ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Troppo piccolo: ${Y.origin} deve essere ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Stringa non valida: deve iniziare con "${z.prefix}"`;
        if (z.format === "ends_with") return `Stringa non valida: deve terminare con "${z.suffix}"`;
        if (z.format === "includes") return `Stringa non valida: deve includere "${z.includes}"`;
        if (z.format === "regex") return `Stringa non valida: deve corrispondere al pattern ${z.pattern}`;
        return `Invalid ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Numero non valido: deve essere un multiplo di ${Y.divisor}`;
      case "unrecognized_keys":
        return `Chiav${Y.keys.length > 1 ? "i" : "e"} non riconosciut${Y.keys.length > 1 ? "e" : "a"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Chiave non valida in ${Y.origin}`;
      case "invalid_union":
        return "Input non valido";
      case "invalid_element":
        return `Valore non valido in ${Y.origin}`;
      default:
        return "Input non valido";
    }
  };
};
function SW() {
  return { localeError: _A() };
}
var xA = () => {
  let $ = { string: { unit: "\u6587\u5B57", verb: "\u3067\u3042\u308B" }, file: { unit: "\u30D0\u30A4\u30C8", verb: "\u3067\u3042\u308B" }, array: { unit: "\u8981\u7D20", verb: "\u3067\u3042\u308B" }, set: { unit: "\u8981\u7D20", verb: "\u3067\u3042\u308B" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u6570\u5024";
      case "object": {
        if (Array.isArray(Y)) return "\u914D\u5217";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u5165\u529B\u5024", email: "\u30E1\u30FC\u30EB\u30A2\u30C9\u30EC\u30B9", url: "URL", emoji: "\u7D75\u6587\u5B57", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO\u65E5\u6642", date: "ISO\u65E5\u4ED8", time: "ISO\u6642\u523B", duration: "ISO\u671F\u9593", ipv4: "IPv4\u30A2\u30C9\u30EC\u30B9", ipv6: "IPv6\u30A2\u30C9\u30EC\u30B9", cidrv4: "IPv4\u7BC4\u56F2", cidrv6: "IPv6\u7BC4\u56F2", base64: "base64\u30A8\u30F3\u30B3\u30FC\u30C9\u6587\u5B57\u5217", base64url: "base64url\u30A8\u30F3\u30B3\u30FC\u30C9\u6587\u5B57\u5217", json_string: "JSON\u6587\u5B57\u5217", e164: "E.164\u756A\u53F7", jwt: "JWT", template_literal: "\u5165\u529B\u5024" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u7121\u52B9\u306A\u5165\u529B: ${Y.expected}\u304C\u671F\u5F85\u3055\u308C\u307E\u3057\u305F\u304C\u3001${J(Y.input)}\u304C\u5165\u529B\u3055\u308C\u307E\u3057\u305F`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u7121\u52B9\u306A\u5165\u529B: ${S(Y.values[0])}\u304C\u671F\u5F85\u3055\u308C\u307E\u3057\u305F`;
        return `\u7121\u52B9\u306A\u9078\u629E: ${M(Y.values, "\u3001")}\u306E\u3044\u305A\u308C\u304B\u3067\u3042\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      case "too_big": {
        let z = Y.inclusive ? "\u4EE5\u4E0B\u3067\u3042\u308B" : "\u3088\u308A\u5C0F\u3055\u3044", W = X(Y.origin);
        if (W) return `\u5927\u304D\u3059\u304E\u308B\u5024: ${Y.origin ?? "\u5024"}\u306F${Y.maximum.toString()}${W.unit ?? "\u8981\u7D20"}${z}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u5927\u304D\u3059\u304E\u308B\u5024: ${Y.origin ?? "\u5024"}\u306F${Y.maximum.toString()}${z}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      }
      case "too_small": {
        let z = Y.inclusive ? "\u4EE5\u4E0A\u3067\u3042\u308B" : "\u3088\u308A\u5927\u304D\u3044", W = X(Y.origin);
        if (W) return `\u5C0F\u3055\u3059\u304E\u308B\u5024: ${Y.origin}\u306F${Y.minimum.toString()}${W.unit}${z}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u5C0F\u3055\u3059\u304E\u308B\u5024: ${Y.origin}\u306F${Y.minimum.toString()}${z}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${z.prefix}"\u3067\u59CB\u307E\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (z.format === "ends_with") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${z.suffix}"\u3067\u7D42\u308F\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (z.format === "includes") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${z.includes}"\u3092\u542B\u3080\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (z.format === "regex") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: \u30D1\u30BF\u30FC\u30F3${z.pattern}\u306B\u4E00\u81F4\u3059\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u7121\u52B9\u306A${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u7121\u52B9\u306A\u6570\u5024: ${Y.divisor}\u306E\u500D\u6570\u3067\u3042\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      case "unrecognized_keys":
        return `\u8A8D\u8B58\u3055\u308C\u3066\u3044\u306A\u3044\u30AD\u30FC${Y.keys.length > 1 ? "\u7FA4" : ""}: ${M(Y.keys, "\u3001")}`;
      case "invalid_key":
        return `${Y.origin}\u5185\u306E\u7121\u52B9\u306A\u30AD\u30FC`;
      case "invalid_union":
        return "\u7121\u52B9\u306A\u5165\u529B";
      case "invalid_element":
        return `${Y.origin}\u5185\u306E\u7121\u52B9\u306A\u5024`;
      default:
        return "\u7121\u52B9\u306A\u5165\u529B";
    }
  };
};
function vW() {
  return { localeError: xA() };
}
var TA = () => {
  let $ = { string: { unit: "\u178F\u17BD\u17A2\u1780\u17D2\u179F\u179A", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, file: { unit: "\u1794\u17C3", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, array: { unit: "\u1792\u17B6\u178F\u17BB", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, set: { unit: "\u1792\u17B6\u178F\u17BB", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "\u1798\u17B7\u1793\u1798\u17C2\u1793\u1787\u17B6\u179B\u17C1\u1781 (NaN)" : "\u179B\u17C1\u1781";
      case "object": {
        if (Array.isArray(Y)) return "\u17A2\u17B6\u179A\u17C1 (Array)";
        if (Y === null) return "\u1782\u17D2\u1798\u17B6\u1793\u178F\u1798\u17D2\u179B\u17C3 (null)";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B", email: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793\u17A2\u17CA\u17B8\u1798\u17C2\u179B", url: "URL", emoji: "\u179F\u1789\u17D2\u1789\u17B6\u17A2\u17B6\u179A\u1798\u17D2\u1798\u178E\u17CD", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u1780\u17B6\u179B\u1794\u179A\u17B7\u1785\u17D2\u1786\u17C1\u1791 \u1793\u17B7\u1784\u1798\u17C9\u17C4\u1784 ISO", date: "\u1780\u17B6\u179B\u1794\u179A\u17B7\u1785\u17D2\u1786\u17C1\u1791 ISO", time: "\u1798\u17C9\u17C4\u1784 ISO", duration: "\u179A\u1799\u17C8\u1796\u17C1\u179B ISO", ipv4: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv4", ipv6: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv6", cidrv4: "\u178A\u17C2\u1793\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv4", cidrv6: "\u178A\u17C2\u1793\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv6", base64: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u17A2\u17CA\u17B7\u1780\u17BC\u178A base64", base64url: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u17A2\u17CA\u17B7\u1780\u17BC\u178A base64url", json_string: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A JSON", e164: "\u179B\u17C1\u1781 E.164", jwt: "JWT", template_literal: "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${Y.expected} \u1794\u17C9\u17BB\u1793\u17D2\u178F\u17C2\u1791\u1791\u17BD\u179B\u1794\u17B6\u1793 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${S(Y.values[0])}`;
        return `\u1787\u1798\u17D2\u179A\u17BE\u179F\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1787\u17B6\u1798\u17BD\u1799\u1780\u17D2\u1793\u17BB\u1784\u1785\u17C6\u178E\u17C4\u1798 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u1792\u17C6\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${Y.origin ?? "\u178F\u1798\u17D2\u179B\u17C3"} ${z} ${Y.maximum.toString()} ${W.unit ?? "\u1792\u17B6\u178F\u17BB"}`;
        return `\u1792\u17C6\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${Y.origin ?? "\u178F\u1798\u17D2\u179B\u17C3"} ${z} ${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u178F\u17BC\u1785\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${Y.origin} ${z} ${Y.minimum.toString()} ${W.unit}`;
        return `\u178F\u17BC\u1785\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${Y.origin} ${z} ${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1785\u17B6\u1794\u17CB\u1795\u17D2\u178F\u17BE\u1798\u178A\u17C4\u1799 "${z.prefix}"`;
        if (z.format === "ends_with") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1794\u1789\u17D2\u1785\u1794\u17CB\u178A\u17C4\u1799 "${z.suffix}"`;
        if (z.format === "includes") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1798\u17B6\u1793 "${z.includes}"`;
        if (z.format === "regex") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u178F\u17C2\u1795\u17D2\u1782\u17BC\u1795\u17D2\u1782\u1784\u1793\u17B9\u1784\u1791\u1798\u17D2\u179A\u1784\u17CB\u178A\u17C2\u179B\u1794\u17B6\u1793\u1780\u17C6\u178E\u178F\u17CB ${z.pattern}`;
        return `\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u179B\u17C1\u1781\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u178F\u17C2\u1787\u17B6\u1796\u17A0\u17BB\u1782\u17BB\u178E\u1793\u17C3 ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u179A\u1780\u1783\u17BE\u1789\u179F\u17C4\u1798\u17B7\u1793\u179F\u17D2\u1782\u17B6\u179B\u17CB\u17D6 ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u179F\u17C4\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u1793\u17C5\u1780\u17D2\u1793\u17BB\u1784 ${Y.origin}`;
      case "invalid_union":
        return "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C";
      case "invalid_element":
        return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u1793\u17C5\u1780\u17D2\u1793\u17BB\u1784 ${Y.origin}`;
      default:
        return "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C";
    }
  };
};
function CW() {
  return { localeError: TA() };
}
var yA = () => {
  let $ = { string: { unit: "\uBB38\uC790", verb: "to have" }, file: { unit: "\uBC14\uC774\uD2B8", verb: "to have" }, array: { unit: "\uAC1C", verb: "to have" }, set: { unit: "\uAC1C", verb: "to have" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\uC785\uB825", email: "\uC774\uBA54\uC77C \uC8FC\uC18C", url: "URL", emoji: "\uC774\uBAA8\uC9C0", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \uB0A0\uC9DC\uC2DC\uAC04", date: "ISO \uB0A0\uC9DC", time: "ISO \uC2DC\uAC04", duration: "ISO \uAE30\uAC04", ipv4: "IPv4 \uC8FC\uC18C", ipv6: "IPv6 \uC8FC\uC18C", cidrv4: "IPv4 \uBC94\uC704", cidrv6: "IPv6 \uBC94\uC704", base64: "base64 \uC778\uCF54\uB529 \uBB38\uC790\uC5F4", base64url: "base64url \uC778\uCF54\uB529 \uBB38\uC790\uC5F4", json_string: "JSON \uBB38\uC790\uC5F4", e164: "E.164 \uBC88\uD638", jwt: "JWT", template_literal: "\uC785\uB825" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\uC798\uBABB\uB41C \uC785\uB825: \uC608\uC0C1 \uD0C0\uC785\uC740 ${Y.expected}, \uBC1B\uC740 \uD0C0\uC785\uC740 ${J(Y.input)}\uC785\uB2C8\uB2E4`;
      case "invalid_value":
        if (Y.values.length === 1) return `\uC798\uBABB\uB41C \uC785\uB825: \uAC12\uC740 ${S(Y.values[0])} \uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4`;
        return `\uC798\uBABB\uB41C \uC635\uC158: ${M(Y.values, "\uB610\uB294 ")} \uC911 \uD558\uB098\uC5EC\uC57C \uD569\uB2C8\uB2E4`;
      case "too_big": {
        let z = Y.inclusive ? "\uC774\uD558" : "\uBBF8\uB9CC", W = z === "\uBBF8\uB9CC" ? "\uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4" : "\uC5EC\uC57C \uD569\uB2C8\uB2E4", G = X(Y.origin), U = G?.unit ?? "\uC694\uC18C";
        if (G) return `${Y.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uD07D\uB2C8\uB2E4: ${Y.maximum.toString()}${U} ${z}${W}`;
        return `${Y.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uD07D\uB2C8\uB2E4: ${Y.maximum.toString()} ${z}${W}`;
      }
      case "too_small": {
        let z = Y.inclusive ? "\uC774\uC0C1" : "\uCD08\uACFC", W = z === "\uC774\uC0C1" ? "\uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4" : "\uC5EC\uC57C \uD569\uB2C8\uB2E4", G = X(Y.origin), U = G?.unit ?? "\uC694\uC18C";
        if (G) return `${Y.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uC791\uC2B5\uB2C8\uB2E4: ${Y.minimum.toString()}${U} ${z}${W}`;
        return `${Y.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uC791\uC2B5\uB2C8\uB2E4: ${Y.minimum.toString()} ${z}${W}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${z.prefix}"(\uC73C)\uB85C \uC2DC\uC791\uD574\uC57C \uD569\uB2C8\uB2E4`;
        if (z.format === "ends_with") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${z.suffix}"(\uC73C)\uB85C \uB05D\uB098\uC57C \uD569\uB2C8\uB2E4`;
        if (z.format === "includes") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${z.includes}"\uC744(\uB97C) \uD3EC\uD568\uD574\uC57C \uD569\uB2C8\uB2E4`;
        if (z.format === "regex") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: \uC815\uADDC\uC2DD ${z.pattern} \uD328\uD134\uACFC \uC77C\uCE58\uD574\uC57C \uD569\uB2C8\uB2E4`;
        return `\uC798\uBABB\uB41C ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\uC798\uBABB\uB41C \uC22B\uC790: ${Y.divisor}\uC758 \uBC30\uC218\uC5EC\uC57C \uD569\uB2C8\uB2E4`;
      case "unrecognized_keys":
        return `\uC778\uC2DD\uD560 \uC218 \uC5C6\uB294 \uD0A4: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\uC798\uBABB\uB41C \uD0A4: ${Y.origin}`;
      case "invalid_union":
        return "\uC798\uBABB\uB41C \uC785\uB825";
      case "invalid_element":
        return `\uC798\uBABB\uB41C \uAC12: ${Y.origin}`;
      default:
        return "\uC798\uBABB\uB41C \uC785\uB825";
    }
  };
};
function kW() {
  return { localeError: yA() };
}
var fA = () => {
  let $ = { string: { unit: "\u0437\u043D\u0430\u0446\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, file: { unit: "\u0431\u0430\u0458\u0442\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, array: { unit: "\u0441\u0442\u0430\u0432\u043A\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, set: { unit: "\u0441\u0442\u0430\u0432\u043A\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0431\u0440\u043E\u0458";
      case "object": {
        if (Array.isArray(Y)) return "\u043D\u0438\u0437\u0430";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0432\u043D\u0435\u0441", email: "\u0430\u0434\u0440\u0435\u0441\u0430 \u043D\u0430 \u0435-\u043F\u043E\u0448\u0442\u0430", url: "URL", emoji: "\u0435\u043C\u043E\u045F\u0438", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0443\u043C \u0438 \u0432\u0440\u0435\u043C\u0435", date: "ISO \u0434\u0430\u0442\u0443\u043C", time: "ISO \u0432\u0440\u0435\u043C\u0435", duration: "ISO \u0432\u0440\u0435\u043C\u0435\u0442\u0440\u0430\u0435\u045A\u0435", ipv4: "IPv4 \u0430\u0434\u0440\u0435\u0441\u0430", ipv6: "IPv6 \u0430\u0434\u0440\u0435\u0441\u0430", cidrv4: "IPv4 \u043E\u043F\u0441\u0435\u0433", cidrv6: "IPv6 \u043E\u043F\u0441\u0435\u0433", base64: "base64-\u0435\u043D\u043A\u043E\u0434\u0438\u0440\u0430\u043D\u0430 \u043D\u0438\u0437\u0430", base64url: "base64url-\u0435\u043D\u043A\u043E\u0434\u0438\u0440\u0430\u043D\u0430 \u043D\u0438\u0437\u0430", json_string: "JSON \u043D\u0438\u0437\u0430", e164: "E.164 \u0431\u0440\u043E\u0458", jwt: "JWT", template_literal: "\u0432\u043D\u0435\u0441" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${Y.expected}, \u043F\u0440\u0438\u043C\u0435\u043D\u043E ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Invalid input: expected ${S(Y.values[0])}`;
        return `\u0413\u0440\u0435\u0448\u0430\u043D\u0430 \u043E\u043F\u0446\u0438\u0458\u0430: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 \u0435\u0434\u043D\u0430 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u0433\u043E\u043B\u0435\u043C: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${Y.origin ?? "\u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442\u0430"} \u0434\u0430 \u0438\u043C\u0430 ${z}${Y.maximum.toString()} ${W.unit ?? "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0438"}`;
        return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u0433\u043E\u043B\u0435\u043C: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${Y.origin ?? "\u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442\u0430"} \u0434\u0430 \u0431\u0438\u0434\u0435 ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u043C\u0430\u043B: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${Y.origin} \u0434\u0430 \u0438\u043C\u0430 ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u043C\u0430\u043B: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${Y.origin} \u0434\u0430 \u0431\u0438\u0434\u0435 ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0437\u0430\u043F\u043E\u0447\u043D\u0443\u0432\u0430 \u0441\u043E "${z.prefix}"`;
        if (z.format === "ends_with") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0437\u0430\u0432\u0440\u0448\u0443\u0432\u0430 \u0441\u043E "${z.suffix}"`;
        if (z.format === "includes") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0432\u043A\u043B\u0443\u0447\u0443\u0432\u0430 "${z.includes}"`;
        if (z.format === "regex") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u043E\u0434\u0433\u043E\u0430\u0440\u0430 \u043D\u0430 \u043F\u0430\u0442\u0435\u0440\u043D\u043E\u0442 ${z.pattern}`;
        return `Invalid ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u0431\u0440\u043E\u0458: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0431\u0438\u0434\u0435 \u0434\u0435\u043B\u0438\u0432 \u0441\u043E ${Y.divisor}`;
      case "unrecognized_keys":
        return `${Y.keys.length > 1 ? "\u041D\u0435\u043F\u0440\u0435\u043F\u043E\u0437\u043D\u0430\u0435\u043D\u0438 \u043A\u043B\u0443\u0447\u0435\u0432\u0438" : "\u041D\u0435\u043F\u0440\u0435\u043F\u043E\u0437\u043D\u0430\u0435\u043D \u043A\u043B\u0443\u0447"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u043A\u043B\u0443\u0447 \u0432\u043E ${Y.origin}`;
      case "invalid_union":
        return "\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441";
      case "invalid_element":
        return `\u0413\u0440\u0435\u0448\u043D\u0430 \u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442 \u0432\u043E ${Y.origin}`;
      default:
        return "\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441";
    }
  };
};
function _W() {
  return { localeError: fA() };
}
var gA = () => {
  let $ = { string: { unit: "aksara", verb: "mempunyai" }, file: { unit: "bait", verb: "mempunyai" }, array: { unit: "elemen", verb: "mempunyai" }, set: { unit: "elemen", verb: "mempunyai" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "nombor";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "input", email: "alamat e-mel", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "tarikh masa ISO", date: "tarikh ISO", time: "masa ISO", duration: "tempoh ISO", ipv4: "alamat IPv4", ipv6: "alamat IPv6", cidrv4: "julat IPv4", cidrv6: "julat IPv6", base64: "string dikodkan base64", base64url: "string dikodkan base64url", json_string: "string JSON", e164: "nombor E.164", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Input tidak sah: dijangka ${Y.expected}, diterima ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Input tidak sah: dijangka ${S(Y.values[0])}`;
        return `Pilihan tidak sah: dijangka salah satu daripada ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Terlalu besar: dijangka ${Y.origin ?? "nilai"} ${W.verb} ${z}${Y.maximum.toString()} ${W.unit ?? "elemen"}`;
        return `Terlalu besar: dijangka ${Y.origin ?? "nilai"} adalah ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Terlalu kecil: dijangka ${Y.origin} ${W.verb} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Terlalu kecil: dijangka ${Y.origin} adalah ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `String tidak sah: mesti bermula dengan "${z.prefix}"`;
        if (z.format === "ends_with") return `String tidak sah: mesti berakhir dengan "${z.suffix}"`;
        if (z.format === "includes") return `String tidak sah: mesti mengandungi "${z.includes}"`;
        if (z.format === "regex") return `String tidak sah: mesti sepadan dengan corak ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} tidak sah`;
      }
      case "not_multiple_of":
        return `Nombor tidak sah: perlu gandaan ${Y.divisor}`;
      case "unrecognized_keys":
        return `Kunci tidak dikenali: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Kunci tidak sah dalam ${Y.origin}`;
      case "invalid_union":
        return "Input tidak sah";
      case "invalid_element":
        return `Nilai tidak sah dalam ${Y.origin}`;
      default:
        return "Input tidak sah";
    }
  };
};
function xW() {
  return { localeError: gA() };
}
var hA = () => {
  let $ = { string: { unit: "tekens" }, file: { unit: "bytes" }, array: { unit: "elementen" }, set: { unit: "elementen" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "getal";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "invoer", email: "emailadres", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datum en tijd", date: "ISO datum", time: "ISO tijd", duration: "ISO duur", ipv4: "IPv4-adres", ipv6: "IPv6-adres", cidrv4: "IPv4-bereik", cidrv6: "IPv6-bereik", base64: "base64-gecodeerde tekst", base64url: "base64 URL-gecodeerde tekst", json_string: "JSON string", e164: "E.164-nummer", jwt: "JWT", template_literal: "invoer" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Ongeldige invoer: verwacht ${Y.expected}, ontving ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Ongeldige invoer: verwacht ${S(Y.values[0])}`;
        return `Ongeldige optie: verwacht \xE9\xE9n van ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Te lang: verwacht dat ${Y.origin ?? "waarde"} ${z}${Y.maximum.toString()} ${W.unit ?? "elementen"} bevat`;
        return `Te lang: verwacht dat ${Y.origin ?? "waarde"} ${z}${Y.maximum.toString()} is`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Te kort: verwacht dat ${Y.origin} ${z}${Y.minimum.toString()} ${W.unit} bevat`;
        return `Te kort: verwacht dat ${Y.origin} ${z}${Y.minimum.toString()} is`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Ongeldige tekst: moet met "${z.prefix}" beginnen`;
        if (z.format === "ends_with") return `Ongeldige tekst: moet op "${z.suffix}" eindigen`;
        if (z.format === "includes") return `Ongeldige tekst: moet "${z.includes}" bevatten`;
        if (z.format === "regex") return `Ongeldige tekst: moet overeenkomen met patroon ${z.pattern}`;
        return `Ongeldig: ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Ongeldig getal: moet een veelvoud van ${Y.divisor} zijn`;
      case "unrecognized_keys":
        return `Onbekende key${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Ongeldige key in ${Y.origin}`;
      case "invalid_union":
        return "Ongeldige invoer";
      case "invalid_element":
        return `Ongeldige waarde in ${Y.origin}`;
      default:
        return "Ongeldige invoer";
    }
  };
};
function TW() {
  return { localeError: hA() };
}
var uA = () => {
  let $ = { string: { unit: "tegn", verb: "\xE5 ha" }, file: { unit: "bytes", verb: "\xE5 ha" }, array: { unit: "elementer", verb: "\xE5 inneholde" }, set: { unit: "elementer", verb: "\xE5 inneholde" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "tall";
      case "object": {
        if (Array.isArray(Y)) return "liste";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "input", email: "e-postadresse", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO dato- og klokkeslett", date: "ISO-dato", time: "ISO-klokkeslett", duration: "ISO-varighet", ipv4: "IPv4-omr\xE5de", ipv6: "IPv6-omr\xE5de", cidrv4: "IPv4-spekter", cidrv6: "IPv6-spekter", base64: "base64-enkodet streng", base64url: "base64url-enkodet streng", json_string: "JSON-streng", e164: "E.164-nummer", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Ugyldig input: forventet ${Y.expected}, fikk ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Ugyldig verdi: forventet ${S(Y.values[0])}`;
        return `Ugyldig valg: forventet en av ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `For stor(t): forventet ${Y.origin ?? "value"} til \xE5 ha ${z}${Y.maximum.toString()} ${W.unit ?? "elementer"}`;
        return `For stor(t): forventet ${Y.origin ?? "value"} til \xE5 ha ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `For lite(n): forventet ${Y.origin} til \xE5 ha ${z}${Y.minimum.toString()} ${W.unit}`;
        return `For lite(n): forventet ${Y.origin} til \xE5 ha ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Ugyldig streng: m\xE5 starte med "${z.prefix}"`;
        if (z.format === "ends_with") return `Ugyldig streng: m\xE5 ende med "${z.suffix}"`;
        if (z.format === "includes") return `Ugyldig streng: m\xE5 inneholde "${z.includes}"`;
        if (z.format === "regex") return `Ugyldig streng: m\xE5 matche m\xF8nsteret ${z.pattern}`;
        return `Ugyldig ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Ugyldig tall: m\xE5 v\xE6re et multiplum av ${Y.divisor}`;
      case "unrecognized_keys":
        return `${Y.keys.length > 1 ? "Ukjente n\xF8kler" : "Ukjent n\xF8kkel"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Ugyldig n\xF8kkel i ${Y.origin}`;
      case "invalid_union":
        return "Ugyldig input";
      case "invalid_element":
        return `Ugyldig verdi i ${Y.origin}`;
      default:
        return "Ugyldig input";
    }
  };
};
function yW() {
  return { localeError: uA() };
}
var mA = () => {
  let $ = { string: { unit: "harf", verb: "olmal\u0131d\u0131r" }, file: { unit: "bayt", verb: "olmal\u0131d\u0131r" }, array: { unit: "unsur", verb: "olmal\u0131d\u0131r" }, set: { unit: "unsur", verb: "olmal\u0131d\u0131r" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "numara";
      case "object": {
        if (Array.isArray(Y)) return "saf";
        if (Y === null) return "gayb";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "giren", email: "epostag\xE2h", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO heng\xE2m\u0131", date: "ISO tarihi", time: "ISO zaman\u0131", duration: "ISO m\xFCddeti", ipv4: "IPv4 ni\u015F\xE2n\u0131", ipv6: "IPv6 ni\u015F\xE2n\u0131", cidrv4: "IPv4 menzili", cidrv6: "IPv6 menzili", base64: "base64-\u015Fifreli metin", base64url: "base64url-\u015Fifreli metin", json_string: "JSON metin", e164: "E.164 say\u0131s\u0131", jwt: "JWT", template_literal: "giren" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `F\xE2sit giren: umulan ${Y.expected}, al\u0131nan ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `F\xE2sit giren: umulan ${S(Y.values[0])}`;
        return `F\xE2sit tercih: m\xFBteberler ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Fazla b\xFCy\xFCk: ${Y.origin ?? "value"}, ${z}${Y.maximum.toString()} ${W.unit ?? "elements"} sahip olmal\u0131yd\u0131.`;
        return `Fazla b\xFCy\xFCk: ${Y.origin ?? "value"}, ${z}${Y.maximum.toString()} olmal\u0131yd\u0131.`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Fazla k\xFC\xE7\xFCk: ${Y.origin}, ${z}${Y.minimum.toString()} ${W.unit} sahip olmal\u0131yd\u0131.`;
        return `Fazla k\xFC\xE7\xFCk: ${Y.origin}, ${z}${Y.minimum.toString()} olmal\u0131yd\u0131.`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `F\xE2sit metin: "${z.prefix}" ile ba\u015Flamal\u0131.`;
        if (z.format === "ends_with") return `F\xE2sit metin: "${z.suffix}" ile bitmeli.`;
        if (z.format === "includes") return `F\xE2sit metin: "${z.includes}" ihtiv\xE2 etmeli.`;
        if (z.format === "regex") return `F\xE2sit metin: ${z.pattern} nak\u015F\u0131na uymal\u0131.`;
        return `F\xE2sit ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `F\xE2sit say\u0131: ${Y.divisor} kat\u0131 olmal\u0131yd\u0131.`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan anahtar ${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `${Y.origin} i\xE7in tan\u0131nmayan anahtar var.`;
      case "invalid_union":
        return "Giren tan\u0131namad\u0131.";
      case "invalid_element":
        return `${Y.origin} i\xE7in tan\u0131nmayan k\u0131ymet var.`;
      default:
        return "K\u0131ymet tan\u0131namad\u0131.";
    }
  };
};
function fW() {
  return { localeError: mA() };
}
var lA = () => {
  let $ = { string: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" }, file: { unit: "\u0628\u0627\u06CC\u067C\u0633", verb: "\u0648\u0644\u0631\u064A" }, array: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" }, set: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0639\u062F\u062F";
      case "object": {
        if (Array.isArray(Y)) return "\u0627\u0631\u06D0";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0648\u0631\u0648\u062F\u064A", email: "\u0628\u0631\u06CC\u069A\u0646\u0627\u0644\u06CC\u06A9", url: "\u06CC\u0648 \u0622\u0631 \u0627\u0644", emoji: "\u0627\u06CC\u0645\u0648\u062C\u064A", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0646\u06CC\u067C\u0647 \u0627\u0648 \u0648\u062E\u062A", date: "\u0646\u06D0\u067C\u0647", time: "\u0648\u062E\u062A", duration: "\u0645\u0648\u062F\u0647", ipv4: "\u062F IPv4 \u067E\u062A\u0647", ipv6: "\u062F IPv6 \u067E\u062A\u0647", cidrv4: "\u062F IPv4 \u0633\u0627\u062D\u0647", cidrv6: "\u062F IPv6 \u0633\u0627\u062D\u0647", base64: "base64-encoded \u0645\u062A\u0646", base64url: "base64url-encoded \u0645\u062A\u0646", json_string: "JSON \u0645\u062A\u0646", e164: "\u062F E.164 \u0634\u0645\u06D0\u0631\u0647", jwt: "JWT", template_literal: "\u0648\u0631\u0648\u062F\u064A" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0646\u0627\u0633\u0645 \u0648\u0631\u0648\u062F\u064A: \u0628\u0627\u06CC\u062F ${Y.expected} \u0648\u0627\u06CC, \u0645\u06AB\u0631 ${J(Y.input)} \u062A\u0631\u0644\u0627\u0633\u0647 \u0634\u0648`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0646\u0627\u0633\u0645 \u0648\u0631\u0648\u062F\u064A: \u0628\u0627\u06CC\u062F ${S(Y.values[0])} \u0648\u0627\u06CC`;
        return `\u0646\u0627\u0633\u0645 \u0627\u0646\u062A\u062E\u0627\u0628: \u0628\u0627\u06CC\u062F \u06CC\u0648 \u0644\u0647 ${M(Y.values, "|")} \u0685\u062E\u0647 \u0648\u0627\u06CC`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u0689\u06CC\u0631 \u0644\u0648\u06CC: ${Y.origin ?? "\u0627\u0631\u0632\u069A\u062A"} \u0628\u0627\u06CC\u062F ${z}${Y.maximum.toString()} ${W.unit ?? "\u0639\u0646\u0635\u0631\u0648\u0646\u0647"} \u0648\u0644\u0631\u064A`;
        return `\u0689\u06CC\u0631 \u0644\u0648\u06CC: ${Y.origin ?? "\u0627\u0631\u0632\u069A\u062A"} \u0628\u0627\u06CC\u062F ${z}${Y.maximum.toString()} \u0648\u064A`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u0689\u06CC\u0631 \u06A9\u0648\u0686\u0646\u06CC: ${Y.origin} \u0628\u0627\u06CC\u062F ${z}${Y.minimum.toString()} ${W.unit} \u0648\u0644\u0631\u064A`;
        return `\u0689\u06CC\u0631 \u06A9\u0648\u0686\u0646\u06CC: ${Y.origin} \u0628\u0627\u06CC\u062F ${z}${Y.minimum.toString()} \u0648\u064A`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F "${z.prefix}" \u0633\u0631\u0647 \u067E\u06CC\u0644 \u0634\u064A`;
        if (z.format === "ends_with") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F "${z.suffix}" \u0633\u0631\u0647 \u067E\u0627\u06CC \u062A\u0647 \u0648\u0631\u0633\u064A\u0696\u064A`;
        if (z.format === "includes") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F "${z.includes}" \u0648\u0644\u0631\u064A`;
        if (z.format === "regex") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F ${z.pattern} \u0633\u0631\u0647 \u0645\u0637\u0627\u0628\u0642\u062A \u0648\u0644\u0631\u064A`;
        return `${Q[z.format] ?? Y.format} \u0646\u0627\u0633\u0645 \u062F\u06CC`;
      }
      case "not_multiple_of":
        return `\u0646\u0627\u0633\u0645 \u0639\u062F\u062F: \u0628\u0627\u06CC\u062F \u062F ${Y.divisor} \u0645\u0636\u0631\u0628 \u0648\u064A`;
      case "unrecognized_keys":
        return `\u0646\u0627\u0633\u0645 ${Y.keys.length > 1 ? "\u06A9\u0644\u06CC\u0689\u0648\u0646\u0647" : "\u06A9\u0644\u06CC\u0689"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u0646\u0627\u0633\u0645 \u06A9\u0644\u06CC\u0689 \u067E\u0647 ${Y.origin} \u06A9\u06D0`;
      case "invalid_union":
        return "\u0646\u0627\u0633\u0645\u0647 \u0648\u0631\u0648\u062F\u064A";
      case "invalid_element":
        return `\u0646\u0627\u0633\u0645 \u0639\u0646\u0635\u0631 \u067E\u0647 ${Y.origin} \u06A9\u06D0`;
      default:
        return "\u0646\u0627\u0633\u0645\u0647 \u0648\u0631\u0648\u062F\u064A";
    }
  };
};
function gW() {
  return { localeError: lA() };
}
var cA = () => {
  let $ = { string: { unit: "znak\xF3w", verb: "mie\u0107" }, file: { unit: "bajt\xF3w", verb: "mie\u0107" }, array: { unit: "element\xF3w", verb: "mie\u0107" }, set: { unit: "element\xF3w", verb: "mie\u0107" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "liczba";
      case "object": {
        if (Array.isArray(Y)) return "tablica";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "wyra\u017Cenie", email: "adres email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data i godzina w formacie ISO", date: "data w formacie ISO", time: "godzina w formacie ISO", duration: "czas trwania ISO", ipv4: "adres IPv4", ipv6: "adres IPv6", cidrv4: "zakres IPv4", cidrv6: "zakres IPv6", base64: "ci\u0105g znak\xF3w zakodowany w formacie base64", base64url: "ci\u0105g znak\xF3w zakodowany w formacie base64url", json_string: "ci\u0105g znak\xF3w w formacie JSON", e164: "liczba E.164", jwt: "JWT", template_literal: "wej\u015Bcie" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Nieprawid\u0142owe dane wej\u015Bciowe: oczekiwano ${Y.expected}, otrzymano ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Nieprawid\u0142owe dane wej\u015Bciowe: oczekiwano ${S(Y.values[0])}`;
        return `Nieprawid\u0142owa opcja: oczekiwano jednej z warto\u015Bci ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Za du\u017Ca warto\u015B\u0107: oczekiwano, \u017Ce ${Y.origin ?? "warto\u015B\u0107"} b\u0119dzie mie\u0107 ${z}${Y.maximum.toString()} ${W.unit ?? "element\xF3w"}`;
        return `Zbyt du\u017C(y/a/e): oczekiwano, \u017Ce ${Y.origin ?? "warto\u015B\u0107"} b\u0119dzie wynosi\u0107 ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Za ma\u0142a warto\u015B\u0107: oczekiwano, \u017Ce ${Y.origin ?? "warto\u015B\u0107"} b\u0119dzie mie\u0107 ${z}${Y.minimum.toString()} ${W.unit ?? "element\xF3w"}`;
        return `Zbyt ma\u0142(y/a/e): oczekiwano, \u017Ce ${Y.origin ?? "warto\u015B\u0107"} b\u0119dzie wynosi\u0107 ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi zaczyna\u0107 si\u0119 od "${z.prefix}"`;
        if (z.format === "ends_with") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi ko\u0144czy\u0107 si\u0119 na "${z.suffix}"`;
        if (z.format === "includes") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi zawiera\u0107 "${z.includes}"`;
        if (z.format === "regex") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi odpowiada\u0107 wzorcowi ${z.pattern}`;
        return `Nieprawid\u0142ow(y/a/e) ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Nieprawid\u0142owa liczba: musi by\u0107 wielokrotno\u015Bci\u0105 ${Y.divisor}`;
      case "unrecognized_keys":
        return `Nierozpoznane klucze${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Nieprawid\u0142owy klucz w ${Y.origin}`;
      case "invalid_union":
        return "Nieprawid\u0142owe dane wej\u015Bciowe";
      case "invalid_element":
        return `Nieprawid\u0142owa warto\u015B\u0107 w ${Y.origin}`;
      default:
        return "Nieprawid\u0142owe dane wej\u015Bciowe";
    }
  };
};
function hW() {
  return { localeError: cA() };
}
var pA = () => {
  let $ = { string: { unit: "caracteres", verb: "ter" }, file: { unit: "bytes", verb: "ter" }, array: { unit: "itens", verb: "ter" }, set: { unit: "itens", verb: "ter" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "n\xFAmero";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "nulo";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "padr\xE3o", email: "endere\xE7o de e-mail", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data e hora ISO", date: "data ISO", time: "hora ISO", duration: "dura\xE7\xE3o ISO", ipv4: "endere\xE7o IPv4", ipv6: "endere\xE7o IPv6", cidrv4: "faixa de IPv4", cidrv6: "faixa de IPv6", base64: "texto codificado em base64", base64url: "URL codificada em base64", json_string: "texto JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Tipo inv\xE1lido: esperado ${Y.expected}, recebido ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Entrada inv\xE1lida: esperado ${S(Y.values[0])}`;
        return `Op\xE7\xE3o inv\xE1lida: esperada uma das ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Muito grande: esperado que ${Y.origin ?? "valor"} tivesse ${z}${Y.maximum.toString()} ${W.unit ?? "elementos"}`;
        return `Muito grande: esperado que ${Y.origin ?? "valor"} fosse ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Muito pequeno: esperado que ${Y.origin} tivesse ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Muito pequeno: esperado que ${Y.origin} fosse ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Texto inv\xE1lido: deve come\xE7ar com "${z.prefix}"`;
        if (z.format === "ends_with") return `Texto inv\xE1lido: deve terminar com "${z.suffix}"`;
        if (z.format === "includes") return `Texto inv\xE1lido: deve incluir "${z.includes}"`;
        if (z.format === "regex") return `Texto inv\xE1lido: deve corresponder ao padr\xE3o ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} inv\xE1lido`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE1lido: deve ser m\xFAltiplo de ${Y.divisor}`;
      case "unrecognized_keys":
        return `Chave${Y.keys.length > 1 ? "s" : ""} desconhecida${Y.keys.length > 1 ? "s" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Chave inv\xE1lida em ${Y.origin}`;
      case "invalid_union":
        return "Entrada inv\xE1lida";
      case "invalid_element":
        return `Valor inv\xE1lido em ${Y.origin}`;
      default:
        return "Campo inv\xE1lido";
    }
  };
};
function uW() {
  return { localeError: pA() };
}
function EN($, X, J, Q) {
  let Y = Math.abs($), z = Y % 10, W = Y % 100;
  if (W >= 11 && W <= 19) return Q;
  if (z === 1) return X;
  if (z >= 2 && z <= 4) return J;
  return Q;
}
var iA = () => {
  let $ = { string: { unit: { one: "\u0441\u0438\u043C\u0432\u043E\u043B", few: "\u0441\u0438\u043C\u0432\u043E\u043B\u0430", many: "\u0441\u0438\u043C\u0432\u043E\u043B\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, file: { unit: { one: "\u0431\u0430\u0439\u0442", few: "\u0431\u0430\u0439\u0442\u0430", many: "\u0431\u0430\u0439\u0442" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, array: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, set: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0447\u0438\u0441\u043B\u043E";
      case "object": {
        if (Array.isArray(Y)) return "\u043C\u0430\u0441\u0441\u0438\u0432";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0432\u0432\u043E\u0434", email: "email \u0430\u0434\u0440\u0435\u0441", url: "URL", emoji: "\u044D\u043C\u043E\u0434\u0437\u0438", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0430 \u0438 \u0432\u0440\u0435\u043C\u044F", date: "ISO \u0434\u0430\u0442\u0430", time: "ISO \u0432\u0440\u0435\u043C\u044F", duration: "ISO \u0434\u043B\u0438\u0442\u0435\u043B\u044C\u043D\u043E\u0441\u0442\u044C", ipv4: "IPv4 \u0430\u0434\u0440\u0435\u0441", ipv6: "IPv6 \u0430\u0434\u0440\u0435\u0441", cidrv4: "IPv4 \u0434\u0438\u0430\u043F\u0430\u0437\u043E\u043D", cidrv6: "IPv6 \u0434\u0438\u0430\u043F\u0430\u0437\u043E\u043D", base64: "\u0441\u0442\u0440\u043E\u043A\u0430 \u0432 \u0444\u043E\u0440\u043C\u0430\u0442\u0435 base64", base64url: "\u0441\u0442\u0440\u043E\u043A\u0430 \u0432 \u0444\u043E\u0440\u043C\u0430\u0442\u0435 base64url", json_string: "JSON \u0441\u0442\u0440\u043E\u043A\u0430", e164: "\u043D\u043E\u043C\u0435\u0440 E.164", jwt: "JWT", template_literal: "\u0432\u0432\u043E\u0434" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0432\u043E\u0434: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C ${Y.expected}, \u043F\u043E\u043B\u0443\u0447\u0435\u043D\u043E ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0432\u043E\u0434: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C ${S(Y.values[0])}`;
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0430\u0440\u0438\u0430\u043D\u0442: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C \u043E\u0434\u043D\u043E \u0438\u0437 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) {
          let G = Number(Y.maximum), U = EN(G, W.unit.one, W.unit.few, W.unit.many);
          return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u0431\u043E\u043B\u044C\u0448\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435"} \u0431\u0443\u0434\u0435\u0442 \u0438\u043C\u0435\u0442\u044C ${z}${Y.maximum.toString()} ${U}`;
        }
        return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u0431\u043E\u043B\u044C\u0448\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435"} \u0431\u0443\u0434\u0435\u0442 ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) {
          let G = Number(Y.minimum), U = EN(G, W.unit.one, W.unit.few, W.unit.many);
          return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u043C\u0430\u043B\u0435\u043D\u044C\u043A\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${Y.origin} \u0431\u0443\u0434\u0435\u0442 \u0438\u043C\u0435\u0442\u044C ${z}${Y.minimum.toString()} ${U}`;
        }
        return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u043C\u0430\u043B\u0435\u043D\u044C\u043A\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${Y.origin} \u0431\u0443\u0434\u0435\u0442 ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u043D\u0430\u0447\u0438\u043D\u0430\u0442\u044C\u0441\u044F \u0441 "${z.prefix}"`;
        if (z.format === "ends_with") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0437\u0430\u043A\u0430\u043D\u0447\u0438\u0432\u0430\u0442\u044C\u0441\u044F \u043D\u0430 "${z.suffix}"`;
        if (z.format === "includes") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0441\u043E\u0434\u0435\u0440\u0436\u0430\u0442\u044C "${z.includes}"`;
        if (z.format === "regex") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0441\u043E\u043E\u0442\u0432\u0435\u0442\u0441\u0442\u0432\u043E\u0432\u0430\u0442\u044C \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${z.pattern}`;
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u043E\u0435 \u0447\u0438\u0441\u043B\u043E: \u0434\u043E\u043B\u0436\u043D\u043E \u0431\u044B\u0442\u044C \u043A\u0440\u0430\u0442\u043D\u044B\u043C ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u0430\u0441\u043F\u043E\u0437\u043D\u0430\u043D\u043D${Y.keys.length > 1 ? "\u044B\u0435" : "\u044B\u0439"} \u043A\u043B\u044E\u0447${Y.keys.length > 1 ? "\u0438" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u043A\u043B\u044E\u0447 \u0432 ${Y.origin}`;
      case "invalid_union":
        return "\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0435 \u0432\u0445\u043E\u0434\u043D\u044B\u0435 \u0434\u0430\u043D\u043D\u044B\u0435";
      case "invalid_element":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435 \u0432 ${Y.origin}`;
      default:
        return "\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0435 \u0432\u0445\u043E\u0434\u043D\u044B\u0435 \u0434\u0430\u043D\u043D\u044B\u0435";
    }
  };
};
function mW() {
  return { localeError: iA() };
}
var nA = () => {
  let $ = { string: { unit: "znakov", verb: "imeti" }, file: { unit: "bajtov", verb: "imeti" }, array: { unit: "elementov", verb: "imeti" }, set: { unit: "elementov", verb: "imeti" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0161tevilo";
      case "object": {
        if (Array.isArray(Y)) return "tabela";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "vnos", email: "e-po\u0161tni naslov", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datum in \u010Das", date: "ISO datum", time: "ISO \u010Das", duration: "ISO trajanje", ipv4: "IPv4 naslov", ipv6: "IPv6 naslov", cidrv4: "obseg IPv4", cidrv6: "obseg IPv6", base64: "base64 kodiran niz", base64url: "base64url kodiran niz", json_string: "JSON niz", e164: "E.164 \u0161tevilka", jwt: "JWT", template_literal: "vnos" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Neveljaven vnos: pri\u010Dakovano ${Y.expected}, prejeto ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Neveljaven vnos: pri\u010Dakovano ${S(Y.values[0])}`;
        return `Neveljavna mo\u017Enost: pri\u010Dakovano eno izmed ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Preveliko: pri\u010Dakovano, da bo ${Y.origin ?? "vrednost"} imelo ${z}${Y.maximum.toString()} ${W.unit ?? "elementov"}`;
        return `Preveliko: pri\u010Dakovano, da bo ${Y.origin ?? "vrednost"} ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Premajhno: pri\u010Dakovano, da bo ${Y.origin} imelo ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Premajhno: pri\u010Dakovano, da bo ${Y.origin} ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Neveljaven niz: mora se za\u010Deti z "${z.prefix}"`;
        if (z.format === "ends_with") return `Neveljaven niz: mora se kon\u010Dati z "${z.suffix}"`;
        if (z.format === "includes") return `Neveljaven niz: mora vsebovati "${z.includes}"`;
        if (z.format === "regex") return `Neveljaven niz: mora ustrezati vzorcu ${z.pattern}`;
        return `Neveljaven ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Neveljavno \u0161tevilo: mora biti ve\u010Dkratnik ${Y.divisor}`;
      case "unrecognized_keys":
        return `Neprepoznan${Y.keys.length > 1 ? "i klju\u010Di" : " klju\u010D"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Neveljaven klju\u010D v ${Y.origin}`;
      case "invalid_union":
        return "Neveljaven vnos";
      case "invalid_element":
        return `Neveljavna vrednost v ${Y.origin}`;
      default:
        return "Neveljaven vnos";
    }
  };
};
function lW() {
  return { localeError: nA() };
}
var dA = () => {
  let $ = { string: { unit: "tecken", verb: "att ha" }, file: { unit: "bytes", verb: "att ha" }, array: { unit: "objekt", verb: "att inneh\xE5lla" }, set: { unit: "objekt", verb: "att inneh\xE5lla" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "antal";
      case "object": {
        if (Array.isArray(Y)) return "lista";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "regulj\xE4rt uttryck", email: "e-postadress", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-datum och tid", date: "ISO-datum", time: "ISO-tid", duration: "ISO-varaktighet", ipv4: "IPv4-intervall", ipv6: "IPv6-intervall", cidrv4: "IPv4-spektrum", cidrv6: "IPv6-spektrum", base64: "base64-kodad str\xE4ng", base64url: "base64url-kodad str\xE4ng", json_string: "JSON-str\xE4ng", e164: "E.164-nummer", jwt: "JWT", template_literal: "mall-literal" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `Ogiltig inmatning: f\xF6rv\xE4ntat ${Y.expected}, fick ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `Ogiltig inmatning: f\xF6rv\xE4ntat ${S(Y.values[0])}`;
        return `Ogiltigt val: f\xF6rv\xE4ntade en av ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `F\xF6r stor(t): f\xF6rv\xE4ntade ${Y.origin ?? "v\xE4rdet"} att ha ${z}${Y.maximum.toString()} ${W.unit ?? "element"}`;
        return `F\xF6r stor(t): f\xF6rv\xE4ntat ${Y.origin ?? "v\xE4rdet"} att ha ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `F\xF6r lite(t): f\xF6rv\xE4ntade ${Y.origin ?? "v\xE4rdet"} att ha ${z}${Y.minimum.toString()} ${W.unit}`;
        return `F\xF6r lite(t): f\xF6rv\xE4ntade ${Y.origin ?? "v\xE4rdet"} att ha ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Ogiltig str\xE4ng: m\xE5ste b\xF6rja med "${z.prefix}"`;
        if (z.format === "ends_with") return `Ogiltig str\xE4ng: m\xE5ste sluta med "${z.suffix}"`;
        if (z.format === "includes") return `Ogiltig str\xE4ng: m\xE5ste inneh\xE5lla "${z.includes}"`;
        if (z.format === "regex") return `Ogiltig str\xE4ng: m\xE5ste matcha m\xF6nstret "${z.pattern}"`;
        return `Ogiltig(t) ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `Ogiltigt tal: m\xE5ste vara en multipel av ${Y.divisor}`;
      case "unrecognized_keys":
        return `${Y.keys.length > 1 ? "Ok\xE4nda nycklar" : "Ok\xE4nd nyckel"}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Ogiltig nyckel i ${Y.origin ?? "v\xE4rdet"}`;
      case "invalid_union":
        return "Ogiltig input";
      case "invalid_element":
        return `Ogiltigt v\xE4rde i ${Y.origin ?? "v\xE4rdet"}`;
      default:
        return "Ogiltig input";
    }
  };
};
function cW() {
  return { localeError: dA() };
}
var rA = () => {
  let $ = { string: { unit: "\u0B8E\u0BB4\u0BC1\u0BA4\u0BCD\u0BA4\u0BC1\u0B95\u0BCD\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, file: { unit: "\u0BAA\u0BC8\u0B9F\u0BCD\u0B9F\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, array: { unit: "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, set: { unit: "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "\u0B8E\u0BA3\u0BCD \u0B85\u0BB2\u0BCD\u0BB2\u0BBE\u0BA4\u0BA4\u0BC1" : "\u0B8E\u0BA3\u0BCD";
      case "object": {
        if (Array.isArray(Y)) return "\u0B85\u0BA3\u0BBF";
        if (Y === null) return "\u0BB5\u0BC6\u0BB1\u0BC1\u0BAE\u0BC8";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1", email: "\u0BAE\u0BBF\u0BA9\u0BCD\u0BA9\u0B9E\u0BCD\u0B9A\u0BB2\u0BCD \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0BA4\u0BC7\u0BA4\u0BBF \u0BA8\u0BC7\u0BB0\u0BAE\u0BCD", date: "ISO \u0BA4\u0BC7\u0BA4\u0BBF", time: "ISO \u0BA8\u0BC7\u0BB0\u0BAE\u0BCD", duration: "ISO \u0B95\u0BBE\u0BB2 \u0B85\u0BB3\u0BB5\u0BC1", ipv4: "IPv4 \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", ipv6: "IPv6 \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", cidrv4: "IPv4 \u0BB5\u0BB0\u0BAE\u0BCD\u0BAA\u0BC1", cidrv6: "IPv6 \u0BB5\u0BB0\u0BAE\u0BCD\u0BAA\u0BC1", base64: "base64-encoded \u0B9A\u0BB0\u0BAE\u0BCD", base64url: "base64url-encoded \u0B9A\u0BB0\u0BAE\u0BCD", json_string: "JSON \u0B9A\u0BB0\u0BAE\u0BCD", e164: "E.164 \u0B8E\u0BA3\u0BCD", jwt: "JWT", template_literal: "input" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${Y.expected}, \u0BAA\u0BC6\u0BB1\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${S(Y.values[0])}`;
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BB5\u0BBF\u0BB0\u0BC1\u0BAA\u0BCD\u0BAA\u0BAE\u0BCD: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${M(Y.values, "|")} \u0B87\u0BB2\u0BCD \u0B92\u0BA9\u0BCD\u0BB1\u0BC1`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u0BAE\u0BBF\u0B95 \u0BAA\u0BC6\u0BB0\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${Y.origin ?? "\u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1"} ${z}${Y.maximum.toString()} ${W.unit ?? "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD"} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BAE\u0BBF\u0B95 \u0BAA\u0BC6\u0BB0\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${Y.origin ?? "\u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1"} ${z}${Y.maximum.toString()} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u0BAE\u0BBF\u0B95\u0B9A\u0BCD \u0B9A\u0BBF\u0BB1\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${Y.origin} ${z}${Y.minimum.toString()} ${W.unit} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BAE\u0BBF\u0B95\u0B9A\u0BCD \u0B9A\u0BBF\u0BB1\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${Y.origin} ${z}${Y.minimum.toString()} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${z.prefix}" \u0B87\u0BB2\u0BCD \u0BA4\u0BCA\u0B9F\u0B99\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (z.format === "ends_with") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${z.suffix}" \u0B87\u0BB2\u0BCD \u0BAE\u0BC1\u0B9F\u0BBF\u0BB5\u0B9F\u0BC8\u0BAF \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (z.format === "includes") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${z.includes}" \u0B90 \u0B89\u0BB3\u0BCD\u0BB3\u0B9F\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (z.format === "regex") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: ${z.pattern} \u0BAE\u0BC1\u0BB1\u0BC8\u0BAA\u0BBE\u0B9F\u0BCD\u0B9F\u0BC1\u0B9F\u0BA9\u0BCD \u0BAA\u0BCA\u0BB0\u0BC1\u0BA8\u0BCD\u0BA4 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B8E\u0BA3\u0BCD: ${Y.divisor} \u0B87\u0BA9\u0BCD \u0BAA\u0BB2\u0BAE\u0BBE\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      case "unrecognized_keys":
        return `\u0B85\u0B9F\u0BC8\u0BAF\u0BBE\u0BB3\u0BAE\u0BCD \u0BA4\u0BC6\u0BB0\u0BBF\u0BAF\u0BBE\u0BA4 \u0BB5\u0BBF\u0B9A\u0BC8${Y.keys.length > 1 ? "\u0B95\u0BB3\u0BCD" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `${Y.origin} \u0B87\u0BB2\u0BCD \u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BB5\u0BBF\u0B9A\u0BC8`;
      case "invalid_union":
        return "\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1";
      case "invalid_element":
        return `${Y.origin} \u0B87\u0BB2\u0BCD \u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1`;
      default:
        return "\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1";
    }
  };
};
function pW() {
  return { localeError: rA() };
}
var oA = () => {
  let $ = { string: { unit: "\u0E15\u0E31\u0E27\u0E2D\u0E31\u0E01\u0E29\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, file: { unit: "\u0E44\u0E1A\u0E15\u0E4C", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, array: { unit: "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, set: { unit: "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "\u0E44\u0E21\u0E48\u0E43\u0E0A\u0E48\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02 (NaN)" : "\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02";
      case "object": {
        if (Array.isArray(Y)) return "\u0E2D\u0E32\u0E23\u0E4C\u0E40\u0E23\u0E22\u0E4C (Array)";
        if (Y === null) return "\u0E44\u0E21\u0E48\u0E21\u0E35\u0E04\u0E48\u0E32 (null)";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E17\u0E35\u0E48\u0E1B\u0E49\u0E2D\u0E19", email: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48\u0E2D\u0E35\u0E40\u0E21\u0E25", url: "URL", emoji: "\u0E2D\u0E34\u0E42\u0E21\u0E08\u0E34", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0E27\u0E31\u0E19\u0E17\u0E35\u0E48\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", date: "\u0E27\u0E31\u0E19\u0E17\u0E35\u0E48\u0E41\u0E1A\u0E1A ISO", time: "\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", duration: "\u0E0A\u0E48\u0E27\u0E07\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", ipv4: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48 IPv4", ipv6: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48 IPv6", cidrv4: "\u0E0A\u0E48\u0E27\u0E07 IP \u0E41\u0E1A\u0E1A IPv4", cidrv6: "\u0E0A\u0E48\u0E27\u0E07 IP \u0E41\u0E1A\u0E1A IPv6", base64: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A Base64", base64url: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A Base64 \u0E2A\u0E33\u0E2B\u0E23\u0E31\u0E1A URL", json_string: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A JSON", e164: "\u0E40\u0E1A\u0E2D\u0E23\u0E4C\u0E42\u0E17\u0E23\u0E28\u0E31\u0E1E\u0E17\u0E4C\u0E23\u0E30\u0E2B\u0E27\u0E48\u0E32\u0E07\u0E1B\u0E23\u0E30\u0E40\u0E17\u0E28 (E.164)", jwt: "\u0E42\u0E17\u0E40\u0E04\u0E19 JWT", template_literal: "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E17\u0E35\u0E48\u0E1B\u0E49\u0E2D\u0E19" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0E1B\u0E23\u0E30\u0E40\u0E20\u0E17\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19 ${Y.expected} \u0E41\u0E15\u0E48\u0E44\u0E14\u0E49\u0E23\u0E31\u0E1A ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0E04\u0E48\u0E32\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19 ${S(Y.values[0])}`;
        return `\u0E15\u0E31\u0E27\u0E40\u0E25\u0E37\u0E2D\u0E01\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19\u0E2B\u0E19\u0E36\u0E48\u0E07\u0E43\u0E19 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "\u0E44\u0E21\u0E48\u0E40\u0E01\u0E34\u0E19" : "\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32", W = X(Y.origin);
        if (W) return `\u0E40\u0E01\u0E34\u0E19\u0E01\u0E33\u0E2B\u0E19\u0E14: ${Y.origin ?? "\u0E04\u0E48\u0E32"} \u0E04\u0E27\u0E23\u0E21\u0E35${z} ${Y.maximum.toString()} ${W.unit ?? "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23"}`;
        return `\u0E40\u0E01\u0E34\u0E19\u0E01\u0E33\u0E2B\u0E19\u0E14: ${Y.origin ?? "\u0E04\u0E48\u0E32"} \u0E04\u0E27\u0E23\u0E21\u0E35${z} ${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? "\u0E2D\u0E22\u0E48\u0E32\u0E07\u0E19\u0E49\u0E2D\u0E22" : "\u0E21\u0E32\u0E01\u0E01\u0E27\u0E48\u0E32", W = X(Y.origin);
        if (W) return `\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32\u0E01\u0E33\u0E2B\u0E19\u0E14: ${Y.origin} \u0E04\u0E27\u0E23\u0E21\u0E35${z} ${Y.minimum.toString()} ${W.unit}`;
        return `\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32\u0E01\u0E33\u0E2B\u0E19\u0E14: ${Y.origin} \u0E04\u0E27\u0E23\u0E21\u0E35${z} ${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E02\u0E36\u0E49\u0E19\u0E15\u0E49\u0E19\u0E14\u0E49\u0E27\u0E22 "${z.prefix}"`;
        if (z.format === "ends_with") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E25\u0E07\u0E17\u0E49\u0E32\u0E22\u0E14\u0E49\u0E27\u0E22 "${z.suffix}"`;
        if (z.format === "includes") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E21\u0E35 "${z.includes}" \u0E2D\u0E22\u0E39\u0E48\u0E43\u0E19\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21`;
        if (z.format === "regex") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E15\u0E49\u0E2D\u0E07\u0E15\u0E23\u0E07\u0E01\u0E31\u0E1A\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E17\u0E35\u0E48\u0E01\u0E33\u0E2B\u0E19\u0E14 ${z.pattern}`;
        return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E15\u0E49\u0E2D\u0E07\u0E40\u0E1B\u0E47\u0E19\u0E08\u0E33\u0E19\u0E27\u0E19\u0E17\u0E35\u0E48\u0E2B\u0E32\u0E23\u0E14\u0E49\u0E27\u0E22 ${Y.divisor} \u0E44\u0E14\u0E49\u0E25\u0E07\u0E15\u0E31\u0E27`;
      case "unrecognized_keys":
        return `\u0E1E\u0E1A\u0E04\u0E35\u0E22\u0E4C\u0E17\u0E35\u0E48\u0E44\u0E21\u0E48\u0E23\u0E39\u0E49\u0E08\u0E31\u0E01: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u0E04\u0E35\u0E22\u0E4C\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07\u0E43\u0E19 ${Y.origin}`;
      case "invalid_union":
        return "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E44\u0E21\u0E48\u0E15\u0E23\u0E07\u0E01\u0E31\u0E1A\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E22\u0E39\u0E40\u0E19\u0E35\u0E22\u0E19\u0E17\u0E35\u0E48\u0E01\u0E33\u0E2B\u0E19\u0E14\u0E44\u0E27\u0E49";
      case "invalid_element":
        return `\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07\u0E43\u0E19 ${Y.origin}`;
      default:
        return "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07";
    }
  };
};
function iW() {
  return { localeError: oA() };
}
var tA = ($) => {
  let X = typeof $;
  switch (X) {
    case "number":
      return Number.isNaN($) ? "NaN" : "number";
    case "object": {
      if (Array.isArray($)) return "array";
      if ($ === null) return "null";
      if (Object.getPrototypeOf($) !== Object.prototype && $.constructor) return $.constructor.name;
    }
  }
  return X;
};
var aA = () => {
  let $ = { string: { unit: "karakter", verb: "olmal\u0131" }, file: { unit: "bayt", verb: "olmal\u0131" }, array: { unit: "\xF6\u011Fe", verb: "olmal\u0131" }, set: { unit: "\xF6\u011Fe", verb: "olmal\u0131" } };
  function X(Q) {
    return $[Q] ?? null;
  }
  let J = { regex: "girdi", email: "e-posta adresi", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO tarih ve saat", date: "ISO tarih", time: "ISO saat", duration: "ISO s\xFCre", ipv4: "IPv4 adresi", ipv6: "IPv6 adresi", cidrv4: "IPv4 aral\u0131\u011F\u0131", cidrv6: "IPv6 aral\u0131\u011F\u0131", base64: "base64 ile \u015Fifrelenmi\u015F metin", base64url: "base64url ile \u015Fifrelenmi\u015F metin", json_string: "JSON dizesi", e164: "E.164 say\u0131s\u0131", jwt: "JWT", template_literal: "\u015Eablon dizesi" };
  return (Q) => {
    switch (Q.code) {
      case "invalid_type":
        return `Ge\xE7ersiz de\u011Fer: beklenen ${Q.expected}, al\u0131nan ${tA(Q.input)}`;
      case "invalid_value":
        if (Q.values.length === 1) return `Ge\xE7ersiz de\u011Fer: beklenen ${S(Q.values[0])}`;
        return `Ge\xE7ersiz se\xE7enek: a\u015Fa\u011F\u0131dakilerden biri olmal\u0131: ${M(Q.values, "|")}`;
      case "too_big": {
        let Y = Q.inclusive ? "<=" : "<", z = X(Q.origin);
        if (z) return `\xC7ok b\xFCy\xFCk: beklenen ${Q.origin ?? "de\u011Fer"} ${Y}${Q.maximum.toString()} ${z.unit ?? "\xF6\u011Fe"}`;
        return `\xC7ok b\xFCy\xFCk: beklenen ${Q.origin ?? "de\u011Fer"} ${Y}${Q.maximum.toString()}`;
      }
      case "too_small": {
        let Y = Q.inclusive ? ">=" : ">", z = X(Q.origin);
        if (z) return `\xC7ok k\xFC\xE7\xFCk: beklenen ${Q.origin} ${Y}${Q.minimum.toString()} ${z.unit}`;
        return `\xC7ok k\xFC\xE7\xFCk: beklenen ${Q.origin} ${Y}${Q.minimum.toString()}`;
      }
      case "invalid_format": {
        let Y = Q;
        if (Y.format === "starts_with") return `Ge\xE7ersiz metin: "${Y.prefix}" ile ba\u015Flamal\u0131`;
        if (Y.format === "ends_with") return `Ge\xE7ersiz metin: "${Y.suffix}" ile bitmeli`;
        if (Y.format === "includes") return `Ge\xE7ersiz metin: "${Y.includes}" i\xE7ermeli`;
        if (Y.format === "regex") return `Ge\xE7ersiz metin: ${Y.pattern} desenine uymal\u0131`;
        return `Ge\xE7ersiz ${J[Y.format] ?? Q.format}`;
      }
      case "not_multiple_of":
        return `Ge\xE7ersiz say\u0131: ${Q.divisor} ile tam b\xF6l\xFCnebilmeli`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan anahtar${Q.keys.length > 1 ? "lar" : ""}: ${M(Q.keys, ", ")}`;
      case "invalid_key":
        return `${Q.origin} i\xE7inde ge\xE7ersiz anahtar`;
      case "invalid_union":
        return "Ge\xE7ersiz de\u011Fer";
      case "invalid_element":
        return `${Q.origin} i\xE7inde ge\xE7ersiz de\u011Fer`;
      default:
        return "Ge\xE7ersiz de\u011Fer";
    }
  };
};
function nW() {
  return { localeError: aA() };
}
var sA = () => {
  let $ = { string: { unit: "\u0441\u0438\u043C\u0432\u043E\u043B\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, file: { unit: "\u0431\u0430\u0439\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, array: { unit: "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, set: { unit: "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0447\u0438\u0441\u043B\u043E";
      case "object": {
        if (Array.isArray(Y)) return "\u043C\u0430\u0441\u0438\u0432";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456", email: "\u0430\u0434\u0440\u0435\u0441\u0430 \u0435\u043B\u0435\u043A\u0442\u0440\u043E\u043D\u043D\u043E\u0457 \u043F\u043E\u0448\u0442\u0438", url: "URL", emoji: "\u0435\u043C\u043E\u0434\u0437\u0456", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0434\u0430\u0442\u0430 \u0442\u0430 \u0447\u0430\u0441 ISO", date: "\u0434\u0430\u0442\u0430 ISO", time: "\u0447\u0430\u0441 ISO", duration: "\u0442\u0440\u0438\u0432\u0430\u043B\u0456\u0441\u0442\u044C ISO", ipv4: "\u0430\u0434\u0440\u0435\u0441\u0430 IPv4", ipv6: "\u0430\u0434\u0440\u0435\u0441\u0430 IPv6", cidrv4: "\u0434\u0456\u0430\u043F\u0430\u0437\u043E\u043D IPv4", cidrv6: "\u0434\u0456\u0430\u043F\u0430\u0437\u043E\u043D IPv6", base64: "\u0440\u044F\u0434\u043E\u043A \u0443 \u043A\u043E\u0434\u0443\u0432\u0430\u043D\u043D\u0456 base64", base64url: "\u0440\u044F\u0434\u043E\u043A \u0443 \u043A\u043E\u0434\u0443\u0432\u0430\u043D\u043D\u0456 base64url", json_string: "\u0440\u044F\u0434\u043E\u043A JSON", e164: "\u043D\u043E\u043C\u0435\u0440 E.164", jwt: "JWT", template_literal: "\u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F ${Y.expected}, \u043E\u0442\u0440\u0438\u043C\u0430\u043D\u043E ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F ${S(Y.values[0])}`;
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0430 \u043E\u043F\u0446\u0456\u044F: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F \u043E\u0434\u043D\u0435 \u0437 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u0432\u0435\u043B\u0438\u043A\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F"} ${W.verb} ${z}${Y.maximum.toString()} ${W.unit ?? "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432"}`;
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u0432\u0435\u043B\u0438\u043A\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${Y.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F"} \u0431\u0443\u0434\u0435 ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u043C\u0430\u043B\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${Y.origin} ${W.verb} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u043C\u0430\u043B\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${Y.origin} \u0431\u0443\u0434\u0435 ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u043F\u043E\u0447\u0438\u043D\u0430\u0442\u0438\u0441\u044F \u0437 "${z.prefix}"`;
        if (z.format === "ends_with") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u0437\u0430\u043A\u0456\u043D\u0447\u0443\u0432\u0430\u0442\u0438\u0441\u044F \u043D\u0430 "${z.suffix}"`;
        if (z.format === "includes") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u043C\u0456\u0441\u0442\u0438\u0442\u0438 "${z.includes}"`;
        if (z.format === "regex") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u0432\u0456\u0434\u043F\u043E\u0432\u0456\u0434\u0430\u0442\u0438 \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${z.pattern}`;
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0435 \u0447\u0438\u0441\u043B\u043E: \u043F\u043E\u0432\u0438\u043D\u043D\u043E \u0431\u0443\u0442\u0438 \u043A\u0440\u0430\u0442\u043D\u0438\u043C ${Y.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u043E\u0437\u043F\u0456\u0437\u043D\u0430\u043D\u0438\u0439 \u043A\u043B\u044E\u0447${Y.keys.length > 1 ? "\u0456" : ""}: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u043A\u043B\u044E\u0447 \u0443 ${Y.origin}`;
      case "invalid_union":
        return "\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456";
      case "invalid_element":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F \u0443 ${Y.origin}`;
      default:
        return "\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456";
    }
  };
};
function dW() {
  return { localeError: sA() };
}
var eA = () => {
  let $ = { string: { unit: "\u062D\u0631\u0648\u0641", verb: "\u06C1\u0648\u0646\u0627" }, file: { unit: "\u0628\u0627\u0626\u0679\u0633", verb: "\u06C1\u0648\u0646\u0627" }, array: { unit: "\u0622\u0626\u0679\u0645\u0632", verb: "\u06C1\u0648\u0646\u0627" }, set: { unit: "\u0622\u0626\u0679\u0645\u0632", verb: "\u06C1\u0648\u0646\u0627" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "\u0646\u0645\u0628\u0631";
      case "object": {
        if (Array.isArray(Y)) return "\u0622\u0631\u06D2";
        if (Y === null) return "\u0646\u0644";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0627\u0646 \u067E\u0679", email: "\u0627\u06CC \u0645\u06CC\u0644 \u0627\u06CC\u0688\u0631\u06CC\u0633", url: "\u06CC\u0648 \u0622\u0631 \u0627\u06CC\u0644", emoji: "\u0627\u06CC\u0645\u0648\u062C\u06CC", uuid: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", uuidv4: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC \u0648\u06CC 4", uuidv6: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC \u0648\u06CC 6", nanoid: "\u0646\u06CC\u0646\u0648 \u0622\u0626\u06CC \u0688\u06CC", guid: "\u062C\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", cuid: "\u0633\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", cuid2: "\u0633\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC 2", ulid: "\u06CC\u0648 \u0627\u06CC\u0644 \u0622\u0626\u06CC \u0688\u06CC", xid: "\u0627\u06CC\u06A9\u0633 \u0622\u0626\u06CC \u0688\u06CC", ksuid: "\u06A9\u06D2 \u0627\u06CC\u0633 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", datetime: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0688\u06CC\u0679 \u0679\u0627\u0626\u0645", date: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u062A\u0627\u0631\u06CC\u062E", time: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0648\u0642\u062A", duration: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0645\u062F\u062A", ipv4: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 4 \u0627\u06CC\u0688\u0631\u06CC\u0633", ipv6: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 6 \u0627\u06CC\u0688\u0631\u06CC\u0633", cidrv4: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 4 \u0631\u06CC\u0646\u062C", cidrv6: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 6 \u0631\u06CC\u0646\u062C", base64: "\u0628\u06CC\u0633 64 \u0627\u0646 \u06A9\u0648\u0688\u0688 \u0633\u0679\u0631\u0646\u06AF", base64url: "\u0628\u06CC\u0633 64 \u06CC\u0648 \u0622\u0631 \u0627\u06CC\u0644 \u0627\u0646 \u06A9\u0648\u0688\u0688 \u0633\u0679\u0631\u0646\u06AF", json_string: "\u062C\u06D2 \u0627\u06CC\u0633 \u0627\u0648 \u0627\u06CC\u0646 \u0633\u0679\u0631\u0646\u06AF", e164: "\u0627\u06CC 164 \u0646\u0645\u0628\u0631", jwt: "\u062C\u06D2 \u0688\u0628\u0644\u06CC\u0648 \u0679\u06CC", template_literal: "\u0627\u0646 \u067E\u0679" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679: ${Y.expected} \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627\u060C ${J(Y.input)} \u0645\u0648\u0635\u0648\u0644 \u06C1\u0648\u0627`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679: ${S(Y.values[0])} \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
        return `\u063A\u0644\u0637 \u0622\u067E\u0634\u0646: ${M(Y.values, "|")} \u0645\u06CC\u06BA \u0633\u06D2 \u0627\u06CC\u06A9 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u0628\u06C1\u062A \u0628\u0691\u0627: ${Y.origin ?? "\u0648\u06CC\u0644\u06CC\u0648"} \u06A9\u06D2 ${z}${Y.maximum.toString()} ${W.unit ?? "\u0639\u0646\u0627\u0635\u0631"} \u06C1\u0648\u0646\u06D2 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u06D2`;
        return `\u0628\u06C1\u062A \u0628\u0691\u0627: ${Y.origin ?? "\u0648\u06CC\u0644\u06CC\u0648"} \u06A9\u0627 ${z}${Y.maximum.toString()} \u06C1\u0648\u0646\u0627 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u0628\u06C1\u062A \u0686\u06BE\u0648\u0679\u0627: ${Y.origin} \u06A9\u06D2 ${z}${Y.minimum.toString()} ${W.unit} \u06C1\u0648\u0646\u06D2 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u06D2`;
        return `\u0628\u06C1\u062A \u0686\u06BE\u0648\u0679\u0627: ${Y.origin} \u06A9\u0627 ${z}${Y.minimum.toString()} \u06C1\u0648\u0646\u0627 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${z.prefix}" \u0633\u06D2 \u0634\u0631\u0648\u0639 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (z.format === "ends_with") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${z.suffix}" \u067E\u0631 \u062E\u062A\u0645 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (z.format === "includes") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${z.includes}" \u0634\u0627\u0645\u0644 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (z.format === "regex") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: \u067E\u06CC\u0679\u0631\u0646 ${z.pattern} \u0633\u06D2 \u0645\u06CC\u0686 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        return `\u063A\u0644\u0637 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u063A\u0644\u0637 \u0646\u0645\u0628\u0631: ${Y.divisor} \u06A9\u0627 \u0645\u0636\u0627\u0639\u0641 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
      case "unrecognized_keys":
        return `\u063A\u06CC\u0631 \u062A\u0633\u0644\u06CC\u0645 \u0634\u062F\u06C1 \u06A9\u06CC${Y.keys.length > 1 ? "\u0632" : ""}: ${M(Y.keys, "\u060C ")}`;
      case "invalid_key":
        return `${Y.origin} \u0645\u06CC\u06BA \u063A\u0644\u0637 \u06A9\u06CC`;
      case "invalid_union":
        return "\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679";
      case "invalid_element":
        return `${Y.origin} \u0645\u06CC\u06BA \u063A\u0644\u0637 \u0648\u06CC\u0644\u06CC\u0648`;
      default:
        return "\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679";
    }
  };
};
function rW() {
  return { localeError: eA() };
}
var $2 = () => {
  let $ = { string: { unit: "k\xFD t\u1EF1", verb: "c\xF3" }, file: { unit: "byte", verb: "c\xF3" }, array: { unit: "ph\u1EA7n t\u1EED", verb: "c\xF3" }, set: { unit: "ph\u1EA7n t\u1EED", verb: "c\xF3" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "s\u1ED1";
      case "object": {
        if (Array.isArray(Y)) return "m\u1EA3ng";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u0111\u1EA7u v\xE0o", email: "\u0111\u1ECBa ch\u1EC9 email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ng\xE0y gi\u1EDD ISO", date: "ng\xE0y ISO", time: "gi\u1EDD ISO", duration: "kho\u1EA3ng th\u1EDDi gian ISO", ipv4: "\u0111\u1ECBa ch\u1EC9 IPv4", ipv6: "\u0111\u1ECBa ch\u1EC9 IPv6", cidrv4: "d\u1EA3i IPv4", cidrv6: "d\u1EA3i IPv6", base64: "chu\u1ED7i m\xE3 h\xF3a base64", base64url: "chu\u1ED7i m\xE3 h\xF3a base64url", json_string: "chu\u1ED7i JSON", e164: "s\u1ED1 E.164", jwt: "JWT", template_literal: "\u0111\u1EA7u v\xE0o" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i ${Y.expected}, nh\u1EADn \u0111\u01B0\u1EE3c ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i ${S(Y.values[0])}`;
        return `T\xF9y ch\u1ECDn kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i m\u1ED9t trong c\xE1c gi\xE1 tr\u1ECB ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `Qu\xE1 l\u1EDBn: mong \u0111\u1EE3i ${Y.origin ?? "gi\xE1 tr\u1ECB"} ${W.verb} ${z}${Y.maximum.toString()} ${W.unit ?? "ph\u1EA7n t\u1EED"}`;
        return `Qu\xE1 l\u1EDBn: mong \u0111\u1EE3i ${Y.origin ?? "gi\xE1 tr\u1ECB"} ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `Qu\xE1 nh\u1ECF: mong \u0111\u1EE3i ${Y.origin} ${W.verb} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `Qu\xE1 nh\u1ECF: mong \u0111\u1EE3i ${Y.origin} ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i b\u1EAFt \u0111\u1EA7u b\u1EB1ng "${z.prefix}"`;
        if (z.format === "ends_with") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i k\u1EBFt th\xFAc b\u1EB1ng "${z.suffix}"`;
        if (z.format === "includes") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i bao g\u1ED3m "${z.includes}"`;
        if (z.format === "regex") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i kh\u1EDBp v\u1EDBi m\u1EABu ${z.pattern}`;
        return `${Q[z.format] ?? Y.format} kh\xF4ng h\u1EE3p l\u1EC7`;
      }
      case "not_multiple_of":
        return `S\u1ED1 kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i l\xE0 b\u1ED9i s\u1ED1 c\u1EE7a ${Y.divisor}`;
      case "unrecognized_keys":
        return `Kh\xF3a kh\xF4ng \u0111\u01B0\u1EE3c nh\u1EADn d\u1EA1ng: ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `Kh\xF3a kh\xF4ng h\u1EE3p l\u1EC7 trong ${Y.origin}`;
      case "invalid_union":
        return "\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7";
      case "invalid_element":
        return `Gi\xE1 tr\u1ECB kh\xF4ng h\u1EE3p l\u1EC7 trong ${Y.origin}`;
      default:
        return "\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7";
    }
  };
};
function oW() {
  return { localeError: $2() };
}
var X2 = () => {
  let $ = { string: { unit: "\u5B57\u7B26", verb: "\u5305\u542B" }, file: { unit: "\u5B57\u8282", verb: "\u5305\u542B" }, array: { unit: "\u9879", verb: "\u5305\u542B" }, set: { unit: "\u9879", verb: "\u5305\u542B" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "\u975E\u6570\u5B57(NaN)" : "\u6570\u5B57";
      case "object": {
        if (Array.isArray(Y)) return "\u6570\u7EC4";
        if (Y === null) return "\u7A7A\u503C(null)";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u8F93\u5165", email: "\u7535\u5B50\u90AE\u4EF6", url: "URL", emoji: "\u8868\u60C5\u7B26\u53F7", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO\u65E5\u671F\u65F6\u95F4", date: "ISO\u65E5\u671F", time: "ISO\u65F6\u95F4", duration: "ISO\u65F6\u957F", ipv4: "IPv4\u5730\u5740", ipv6: "IPv6\u5730\u5740", cidrv4: "IPv4\u7F51\u6BB5", cidrv6: "IPv6\u7F51\u6BB5", base64: "base64\u7F16\u7801\u5B57\u7B26\u4E32", base64url: "base64url\u7F16\u7801\u5B57\u7B26\u4E32", json_string: "JSON\u5B57\u7B26\u4E32", e164: "E.164\u53F7\u7801", jwt: "JWT", template_literal: "\u8F93\u5165" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u65E0\u6548\u8F93\u5165\uFF1A\u671F\u671B ${Y.expected}\uFF0C\u5B9E\u9645\u63A5\u6536 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u65E0\u6548\u8F93\u5165\uFF1A\u671F\u671B ${S(Y.values[0])}`;
        return `\u65E0\u6548\u9009\u9879\uFF1A\u671F\u671B\u4EE5\u4E0B\u4E4B\u4E00 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u6570\u503C\u8FC7\u5927\uFF1A\u671F\u671B ${Y.origin ?? "\u503C"} ${z}${Y.maximum.toString()} ${W.unit ?? "\u4E2A\u5143\u7D20"}`;
        return `\u6570\u503C\u8FC7\u5927\uFF1A\u671F\u671B ${Y.origin ?? "\u503C"} ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u6570\u503C\u8FC7\u5C0F\uFF1A\u671F\u671B ${Y.origin} ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\u6570\u503C\u8FC7\u5C0F\uFF1A\u671F\u671B ${Y.origin} ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u4EE5 "${z.prefix}" \u5F00\u5934`;
        if (z.format === "ends_with") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u4EE5 "${z.suffix}" \u7ED3\u5C3E`;
        if (z.format === "includes") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u5305\u542B "${z.includes}"`;
        if (z.format === "regex") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u6EE1\u8DB3\u6B63\u5219\u8868\u8FBE\u5F0F ${z.pattern}`;
        return `\u65E0\u6548${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u65E0\u6548\u6570\u5B57\uFF1A\u5FC5\u987B\u662F ${Y.divisor} \u7684\u500D\u6570`;
      case "unrecognized_keys":
        return `\u51FA\u73B0\u672A\u77E5\u7684\u952E(key): ${M(Y.keys, ", ")}`;
      case "invalid_key":
        return `${Y.origin} \u4E2D\u7684\u952E(key)\u65E0\u6548`;
      case "invalid_union":
        return "\u65E0\u6548\u8F93\u5165";
      case "invalid_element":
        return `${Y.origin} \u4E2D\u5305\u542B\u65E0\u6548\u503C(value)`;
      default:
        return "\u65E0\u6548\u8F93\u5165";
    }
  };
};
function tW() {
  return { localeError: X2() };
}
var J2 = () => {
  let $ = { string: { unit: "\u5B57\u5143", verb: "\u64C1\u6709" }, file: { unit: "\u4F4D\u5143\u7D44", verb: "\u64C1\u6709" }, array: { unit: "\u9805\u76EE", verb: "\u64C1\u6709" }, set: { unit: "\u9805\u76EE", verb: "\u64C1\u6709" } };
  function X(Y) {
    return $[Y] ?? null;
  }
  let J = (Y) => {
    let z = typeof Y;
    switch (z) {
      case "number":
        return Number.isNaN(Y) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(Y)) return "array";
        if (Y === null) return "null";
        if (Object.getPrototypeOf(Y) !== Object.prototype && Y.constructor) return Y.constructor.name;
      }
    }
    return z;
  }, Q = { regex: "\u8F38\u5165", email: "\u90F5\u4EF6\u5730\u5740", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u65E5\u671F\u6642\u9593", date: "ISO \u65E5\u671F", time: "ISO \u6642\u9593", duration: "ISO \u671F\u9593", ipv4: "IPv4 \u4F4D\u5740", ipv6: "IPv6 \u4F4D\u5740", cidrv4: "IPv4 \u7BC4\u570D", cidrv6: "IPv6 \u7BC4\u570D", base64: "base64 \u7DE8\u78BC\u5B57\u4E32", base64url: "base64url \u7DE8\u78BC\u5B57\u4E32", json_string: "JSON \u5B57\u4E32", e164: "E.164 \u6578\u503C", jwt: "JWT", template_literal: "\u8F38\u5165" };
  return (Y) => {
    switch (Y.code) {
      case "invalid_type":
        return `\u7121\u6548\u7684\u8F38\u5165\u503C\uFF1A\u9810\u671F\u70BA ${Y.expected}\uFF0C\u4F46\u6536\u5230 ${J(Y.input)}`;
      case "invalid_value":
        if (Y.values.length === 1) return `\u7121\u6548\u7684\u8F38\u5165\u503C\uFF1A\u9810\u671F\u70BA ${S(Y.values[0])}`;
        return `\u7121\u6548\u7684\u9078\u9805\uFF1A\u9810\u671F\u70BA\u4EE5\u4E0B\u5176\u4E2D\u4E4B\u4E00 ${M(Y.values, "|")}`;
      case "too_big": {
        let z = Y.inclusive ? "<=" : "<", W = X(Y.origin);
        if (W) return `\u6578\u503C\u904E\u5927\uFF1A\u9810\u671F ${Y.origin ?? "\u503C"} \u61C9\u70BA ${z}${Y.maximum.toString()} ${W.unit ?? "\u500B\u5143\u7D20"}`;
        return `\u6578\u503C\u904E\u5927\uFF1A\u9810\u671F ${Y.origin ?? "\u503C"} \u61C9\u70BA ${z}${Y.maximum.toString()}`;
      }
      case "too_small": {
        let z = Y.inclusive ? ">=" : ">", W = X(Y.origin);
        if (W) return `\u6578\u503C\u904E\u5C0F\uFF1A\u9810\u671F ${Y.origin} \u61C9\u70BA ${z}${Y.minimum.toString()} ${W.unit}`;
        return `\u6578\u503C\u904E\u5C0F\uFF1A\u9810\u671F ${Y.origin} \u61C9\u70BA ${z}${Y.minimum.toString()}`;
      }
      case "invalid_format": {
        let z = Y;
        if (z.format === "starts_with") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u4EE5 "${z.prefix}" \u958B\u982D`;
        if (z.format === "ends_with") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u4EE5 "${z.suffix}" \u7D50\u5C3E`;
        if (z.format === "includes") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u5305\u542B "${z.includes}"`;
        if (z.format === "regex") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u7B26\u5408\u683C\u5F0F ${z.pattern}`;
        return `\u7121\u6548\u7684 ${Q[z.format] ?? Y.format}`;
      }
      case "not_multiple_of":
        return `\u7121\u6548\u7684\u6578\u5B57\uFF1A\u5FC5\u9808\u70BA ${Y.divisor} \u7684\u500D\u6578`;
      case "unrecognized_keys":
        return `\u7121\u6CD5\u8B58\u5225\u7684\u9375\u503C${Y.keys.length > 1 ? "\u5011" : ""}\uFF1A${M(Y.keys, "\u3001")}`;
      case "invalid_key":
        return `${Y.origin} \u4E2D\u6709\u7121\u6548\u7684\u9375\u503C`;
      case "invalid_union":
        return "\u7121\u6548\u7684\u8F38\u5165\u503C";
      case "invalid_element":
        return `${Y.origin} \u4E2D\u6709\u7121\u6548\u7684\u503C`;
      default:
        return "\u7121\u6548\u7684\u8F38\u5165\u503C";
    }
  };
};
function aW() {
  return { localeError: J2() };
}
var eY = Symbol("ZodOutput");
var $7 = Symbol("ZodInput");
var I8 = class {
  constructor() {
    this._map = /* @__PURE__ */ new WeakMap(), this._idmap = /* @__PURE__ */ new Map();
  }
  add($, ...X) {
    let J = X[0];
    if (this._map.set($, J), J && typeof J === "object" && "id" in J) {
      if (this._idmap.has(J.id)) throw Error(`ID ${J.id} already exists in the registry`);
      this._idmap.set(J.id, $);
    }
    return this;
  }
  remove($) {
    return this._map.delete($), this;
  }
  get($) {
    let X = $._zod.parent;
    if (X) {
      let J = { ...this.get(X) ?? {} };
      return delete J.id, { ...J, ...this._map.get($) };
    }
    return this._map.get($);
  }
  has($) {
    return this._map.has($);
  }
};
function A8() {
  return new I8();
}
var X6 = A8();
function X7($, X) {
  return new $({ type: "string", ...Z(X) });
}
function sW($, X) {
  return new $({ type: "string", coerce: true, ...Z(X) });
}
function b8($, X) {
  return new $({ type: "string", format: "email", check: "string_format", abort: false, ...Z(X) });
}
function I0($, X) {
  return new $({ type: "string", format: "guid", check: "string_format", abort: false, ...Z(X) });
}
function P8($, X) {
  return new $({ type: "string", format: "uuid", check: "string_format", abort: false, ...Z(X) });
}
function Z8($, X) {
  return new $({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v4", ...Z(X) });
}
function E8($, X) {
  return new $({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v6", ...Z(X) });
}
function R8($, X) {
  return new $({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v7", ...Z(X) });
}
function S8($, X) {
  return new $({ type: "string", format: "url", check: "string_format", abort: false, ...Z(X) });
}
function v8($, X) {
  return new $({ type: "string", format: "emoji", check: "string_format", abort: false, ...Z(X) });
}
function C8($, X) {
  return new $({ type: "string", format: "nanoid", check: "string_format", abort: false, ...Z(X) });
}
function k8($, X) {
  return new $({ type: "string", format: "cuid", check: "string_format", abort: false, ...Z(X) });
}
function _8($, X) {
  return new $({ type: "string", format: "cuid2", check: "string_format", abort: false, ...Z(X) });
}
function x8($, X) {
  return new $({ type: "string", format: "ulid", check: "string_format", abort: false, ...Z(X) });
}
function T8($, X) {
  return new $({ type: "string", format: "xid", check: "string_format", abort: false, ...Z(X) });
}
function y8($, X) {
  return new $({ type: "string", format: "ksuid", check: "string_format", abort: false, ...Z(X) });
}
function f8($, X) {
  return new $({ type: "string", format: "ipv4", check: "string_format", abort: false, ...Z(X) });
}
function g8($, X) {
  return new $({ type: "string", format: "ipv6", check: "string_format", abort: false, ...Z(X) });
}
function h8($, X) {
  return new $({ type: "string", format: "cidrv4", check: "string_format", abort: false, ...Z(X) });
}
function u8($, X) {
  return new $({ type: "string", format: "cidrv6", check: "string_format", abort: false, ...Z(X) });
}
function m8($, X) {
  return new $({ type: "string", format: "base64", check: "string_format", abort: false, ...Z(X) });
}
function l8($, X) {
  return new $({ type: "string", format: "base64url", check: "string_format", abort: false, ...Z(X) });
}
function c8($, X) {
  return new $({ type: "string", format: "e164", check: "string_format", abort: false, ...Z(X) });
}
function p8($, X) {
  return new $({ type: "string", format: "jwt", check: "string_format", abort: false, ...Z(X) });
}
var J7 = { Any: null, Minute: -1, Second: 0, Millisecond: 3, Microsecond: 6 };
function eW($, X) {
  return new $({ type: "string", format: "datetime", check: "string_format", offset: false, local: false, precision: null, ...Z(X) });
}
function $G($, X) {
  return new $({ type: "string", format: "date", check: "string_format", ...Z(X) });
}
function XG($, X) {
  return new $({ type: "string", format: "time", check: "string_format", precision: null, ...Z(X) });
}
function JG($, X) {
  return new $({ type: "string", format: "duration", check: "string_format", ...Z(X) });
}
function Y7($, X) {
  return new $({ type: "number", checks: [], ...Z(X) });
}
function YG($, X) {
  return new $({ type: "number", coerce: true, checks: [], ...Z(X) });
}
function Q7($, X) {
  return new $({ type: "number", check: "number_format", abort: false, format: "safeint", ...Z(X) });
}
function z7($, X) {
  return new $({ type: "number", check: "number_format", abort: false, format: "float32", ...Z(X) });
}
function W7($, X) {
  return new $({ type: "number", check: "number_format", abort: false, format: "float64", ...Z(X) });
}
function G7($, X) {
  return new $({ type: "number", check: "number_format", abort: false, format: "int32", ...Z(X) });
}
function U7($, X) {
  return new $({ type: "number", check: "number_format", abort: false, format: "uint32", ...Z(X) });
}
function H7($, X) {
  return new $({ type: "boolean", ...Z(X) });
}
function QG($, X) {
  return new $({ type: "boolean", coerce: true, ...Z(X) });
}
function K7($, X) {
  return new $({ type: "bigint", ...Z(X) });
}
function zG($, X) {
  return new $({ type: "bigint", coerce: true, ...Z(X) });
}
function N7($, X) {
  return new $({ type: "bigint", check: "bigint_format", abort: false, format: "int64", ...Z(X) });
}
function V7($, X) {
  return new $({ type: "bigint", check: "bigint_format", abort: false, format: "uint64", ...Z(X) });
}
function O7($, X) {
  return new $({ type: "symbol", ...Z(X) });
}
function w7($, X) {
  return new $({ type: "undefined", ...Z(X) });
}
function B7($, X) {
  return new $({ type: "null", ...Z(X) });
}
function q7($) {
  return new $({ type: "any" });
}
function A1($) {
  return new $({ type: "unknown" });
}
function D7($, X) {
  return new $({ type: "never", ...Z(X) });
}
function L7($, X) {
  return new $({ type: "void", ...Z(X) });
}
function j7($, X) {
  return new $({ type: "date", ...Z(X) });
}
function WG($, X) {
  return new $({ type: "date", coerce: true, ...Z(X) });
}
function F7($, X) {
  return new $({ type: "nan", ...Z(X) });
}
function K4($, X) {
  return new sJ({ check: "less_than", ...Z(X), value: $, inclusive: false });
}
function I6($, X) {
  return new sJ({ check: "less_than", ...Z(X), value: $, inclusive: true });
}
function N4($, X) {
  return new eJ({ check: "greater_than", ...Z(X), value: $, inclusive: false });
}
function J6($, X) {
  return new eJ({ check: "greater_than", ...Z(X), value: $, inclusive: true });
}
function GG($) {
  return N4(0, $);
}
function UG($) {
  return K4(0, $);
}
function HG($) {
  return I6(0, $);
}
function KG($) {
  return J6(0, $);
}
function b1($, X) {
  return new c5({ check: "multiple_of", ...Z(X), value: $ });
}
function A0($, X) {
  return new n5({ check: "max_size", ...Z(X), maximum: $ });
}
function P1($, X) {
  return new d5({ check: "min_size", ...Z(X), minimum: $ });
}
function i8($, X) {
  return new r5({ check: "size_equals", ...Z(X), size: $ });
}
function b0($, X) {
  return new o5({ check: "max_length", ...Z(X), maximum: $ });
}
function f4($, X) {
  return new t5({ check: "min_length", ...Z(X), minimum: $ });
}
function P0($, X) {
  return new a5({ check: "length_equals", ...Z(X), length: $ });
}
function n8($, X) {
  return new s5({ check: "string_format", format: "regex", ...Z(X), pattern: $ });
}
function d8($) {
  return new e5({ check: "string_format", format: "lowercase", ...Z($) });
}
function r8($) {
  return new $W({ check: "string_format", format: "uppercase", ...Z($) });
}
function o8($, X) {
  return new XW({ check: "string_format", format: "includes", ...Z(X), includes: $ });
}
function t8($, X) {
  return new JW({ check: "string_format", format: "starts_with", ...Z(X), prefix: $ });
}
function a8($, X) {
  return new YW({ check: "string_format", format: "ends_with", ...Z(X), suffix: $ });
}
function NG($, X, J) {
  return new QW({ check: "property", property: $, schema: X, ...Z(J) });
}
function s8($, X) {
  return new zW({ check: "mime_type", mime: $, ...Z(X) });
}
function V4($) {
  return new WW({ check: "overwrite", tx: $ });
}
function e8($) {
  return V4((X) => X.normalize($));
}
function $9() {
  return V4(($) => $.trim());
}
function X9() {
  return V4(($) => $.toLowerCase());
}
function J9() {
  return V4(($) => $.toUpperCase());
}
function Y9($, X, J) {
  return new $({ type: "array", element: X, ...Z(J) });
}
function Y2($, X, J) {
  return new $({ type: "union", options: X, ...Z(J) });
}
function Q2($, X, J, Q) {
  return new $({ type: "union", options: J, discriminator: X, ...Z(Q) });
}
function z2($, X, J) {
  return new $({ type: "intersection", left: X, right: J });
}
function VG($, X, J, Q) {
  let Y = J instanceof i;
  return new $({ type: "tuple", items: X, rest: Y ? J : null, ...Z(Y ? Q : J) });
}
function W2($, X, J, Q) {
  return new $({ type: "record", keyType: X, valueType: J, ...Z(Q) });
}
function G2($, X, J, Q) {
  return new $({ type: "map", keyType: X, valueType: J, ...Z(Q) });
}
function U2($, X, J) {
  return new $({ type: "set", valueType: X, ...Z(J) });
}
function H2($, X, J) {
  let Q = Array.isArray(X) ? Object.fromEntries(X.map((Y) => [Y, Y])) : X;
  return new $({ type: "enum", entries: Q, ...Z(J) });
}
function K2($, X, J) {
  return new $({ type: "enum", entries: X, ...Z(J) });
}
function N2($, X, J) {
  return new $({ type: "literal", values: Array.isArray(X) ? X : [X], ...Z(J) });
}
function M7($, X) {
  return new $({ type: "file", ...Z(X) });
}
function V2($, X) {
  return new $({ type: "transform", transform: X });
}
function O2($, X) {
  return new $({ type: "optional", innerType: X });
}
function w2($, X) {
  return new $({ type: "nullable", innerType: X });
}
function B2($, X, J) {
  return new $({ type: "default", innerType: X, get defaultValue() {
    return typeof J === "function" ? J() : J;
  } });
}
function q2($, X, J) {
  return new $({ type: "nonoptional", innerType: X, ...Z(J) });
}
function D2($, X) {
  return new $({ type: "success", innerType: X });
}
function L2($, X, J) {
  return new $({ type: "catch", innerType: X, catchValue: typeof J === "function" ? J : () => J });
}
function j2($, X, J) {
  return new $({ type: "pipe", in: X, out: J });
}
function F2($, X) {
  return new $({ type: "readonly", innerType: X });
}
function M2($, X, J) {
  return new $({ type: "template_literal", parts: X, ...Z(J) });
}
function I2($, X) {
  return new $({ type: "lazy", getter: X });
}
function A2($, X) {
  return new $({ type: "promise", innerType: X });
}
function I7($, X, J) {
  let Q = Z(J);
  return Q.abort ?? (Q.abort = true), new $({ type: "custom", check: "custom", fn: X, ...Q });
}
function A7($, X, J) {
  return new $({ type: "custom", check: "custom", fn: X, ...Z(J) });
}
function b7($, X) {
  let J = Z(X), Q = J.truthy ?? ["true", "1", "yes", "on", "y", "enabled"], Y = J.falsy ?? ["false", "0", "no", "off", "n", "disabled"];
  if (J.case !== "sensitive") Q = Q.map((w) => typeof w === "string" ? w.toLowerCase() : w), Y = Y.map((w) => typeof w === "string" ? w.toLowerCase() : w);
  let z = new Set(Q), W = new Set(Y), G = $.Pipe ?? F0, U = $.Boolean ?? D0, H = $.String ?? T4, V = new ($.Transform ?? j0)({ type: "transform", transform: (w, B) => {
    let L = w;
    if (J.case !== "sensitive") L = L.toLowerCase();
    if (z.has(L)) return true;
    else if (W.has(L)) return false;
    else return B.issues.push({ code: "invalid_value", expected: "stringbool", values: [...z, ...W], input: B.value, inst: V }), {};
  }, error: J.error }), O = new G({ type: "pipe", in: new H({ type: "string", error: J.error }), out: V, error: J.error });
  return new G({ type: "pipe", in: O, out: new U({ type: "boolean", error: J.error }), error: J.error });
}
function P7($, X, J, Q = {}) {
  let Y = Z(Q), z = { ...Z(Q), check: "string_format", type: "string", format: X, fn: typeof J === "function" ? J : (G) => J.test(G), ...Y };
  if (J instanceof RegExp) z.pattern = J;
  return new $(z);
}
var OG = class {
  constructor($) {
    this._def = $, this.def = $;
  }
  implement($) {
    if (typeof $ !== "function") throw Error("implement() must be called with a function");
    let X = (...J) => {
      let Q = this._def.input ? j1(this._def.input, J, void 0, { callee: X }) : J;
      if (!Array.isArray(Q)) throw Error("Invalid arguments schema: not an array or tuple schema.");
      let Y = $(...Q);
      return this._def.output ? j1(this._def.output, Y, void 0, { callee: X }) : Y;
    };
    return X;
  }
  implementAsync($) {
    if (typeof $ !== "function") throw Error("implement() must be called with a function");
    let X = async (...J) => {
      let Q = this._def.input ? await F1(this._def.input, J, void 0, { callee: X }) : J;
      if (!Array.isArray(Q)) throw Error("Invalid arguments schema: not an array or tuple schema.");
      let Y = await $(...Q);
      return this._def.output ? F1(this._def.output, Y, void 0, { callee: X }) : Y;
    };
    return X;
  }
  input(...$) {
    let X = this.constructor;
    if (Array.isArray($[0])) return new X({ type: "function", input: new y4({ type: "tuple", items: $[0], rest: $[1] }), output: this._def.output });
    return new X({ type: "function", input: $[0], output: this._def.output });
  }
  output($) {
    return new this.constructor({ type: "function", input: this._def.input, output: $ });
  }
};
function Z7($) {
  return new OG({ type: "function", input: Array.isArray($?.input) ? VG(y4, $?.input) : $?.input ?? Y9(L0, A1(I1)), output: $?.output ?? A1(I1) });
}
var E7 = class {
  constructor($) {
    this.counter = 0, this.metadataRegistry = $?.metadata ?? X6, this.target = $?.target ?? "draft-2020-12", this.unrepresentable = $?.unrepresentable ?? "throw", this.override = $?.override ?? (() => {
    }), this.io = $?.io ?? "output", this.seen = /* @__PURE__ */ new Map();
  }
  process($, X = { path: [], schemaPath: [] }) {
    var J;
    let Q = $._zod.def, Y = { guid: "uuid", url: "uri", datetime: "date-time", json_string: "json-string", regex: "" }, z = this.seen.get($);
    if (z) {
      if (z.count++, X.schemaPath.includes($)) z.cycle = X.path;
      return z.schema;
    }
    let W = { schema: {}, count: 1, cycle: void 0, path: X.path };
    this.seen.set($, W);
    let G = $._zod.toJSONSchema?.();
    if (G) W.schema = G;
    else {
      let K = { ...X, schemaPath: [...X.schemaPath, $], path: X.path }, V = $._zod.parent;
      if (V) W.ref = V, this.process(V, K), this.seen.get(V).isParent = true;
      else {
        let O = W.schema;
        switch (Q.type) {
          case "string": {
            let N = O;
            N.type = "string";
            let { minimum: w, maximum: B, format: L, patterns: j, contentEncoding: I } = $._zod.bag;
            if (typeof w === "number") N.minLength = w;
            if (typeof B === "number") N.maxLength = B;
            if (L) {
              if (N.format = Y[L] ?? L, N.format === "") delete N.format;
            }
            if (I) N.contentEncoding = I;
            if (j && j.size > 0) {
              let b = [...j];
              if (b.length === 1) N.pattern = b[0].source;
              else if (b.length > 1) W.schema.allOf = [...b.map((x) => ({ ...this.target === "draft-7" ? { type: "string" } : {}, pattern: x.source }))];
            }
            break;
          }
          case "number": {
            let N = O, { minimum: w, maximum: B, format: L, multipleOf: j, exclusiveMaximum: I, exclusiveMinimum: b } = $._zod.bag;
            if (typeof L === "string" && L.includes("int")) N.type = "integer";
            else N.type = "number";
            if (typeof b === "number") N.exclusiveMinimum = b;
            if (typeof w === "number") {
              if (N.minimum = w, typeof b === "number") if (b >= w) delete N.minimum;
              else delete N.exclusiveMinimum;
            }
            if (typeof I === "number") N.exclusiveMaximum = I;
            if (typeof B === "number") {
              if (N.maximum = B, typeof I === "number") if (I <= B) delete N.maximum;
              else delete N.exclusiveMaximum;
            }
            if (typeof j === "number") N.multipleOf = j;
            break;
          }
          case "boolean": {
            let N = O;
            N.type = "boolean";
            break;
          }
          case "bigint": {
            if (this.unrepresentable === "throw") throw Error("BigInt cannot be represented in JSON Schema");
            break;
          }
          case "symbol": {
            if (this.unrepresentable === "throw") throw Error("Symbols cannot be represented in JSON Schema");
            break;
          }
          case "null": {
            O.type = "null";
            break;
          }
          case "any":
            break;
          case "unknown":
            break;
          case "undefined":
          case "never": {
            O.not = {};
            break;
          }
          case "void": {
            if (this.unrepresentable === "throw") throw Error("Void cannot be represented in JSON Schema");
            break;
          }
          case "date": {
            if (this.unrepresentable === "throw") throw Error("Date cannot be represented in JSON Schema");
            break;
          }
          case "array": {
            let N = O, { minimum: w, maximum: B } = $._zod.bag;
            if (typeof w === "number") N.minItems = w;
            if (typeof B === "number") N.maxItems = B;
            N.type = "array", N.items = this.process(Q.element, { ...K, path: [...K.path, "items"] });
            break;
          }
          case "object": {
            let N = O;
            N.type = "object", N.properties = {};
            let w = Q.shape;
            for (let j in w) N.properties[j] = this.process(w[j], { ...K, path: [...K.path, "properties", j] });
            let B = new Set(Object.keys(w)), L = new Set([...B].filter((j) => {
              let I = Q.shape[j]._zod;
              if (this.io === "input") return I.optin === void 0;
              else return I.optout === void 0;
            }));
            if (L.size > 0) N.required = Array.from(L);
            if (Q.catchall?._zod.def.type === "never") N.additionalProperties = false;
            else if (!Q.catchall) {
              if (this.io === "output") N.additionalProperties = false;
            } else if (Q.catchall) N.additionalProperties = this.process(Q.catchall, { ...K, path: [...K.path, "additionalProperties"] });
            break;
          }
          case "union": {
            let N = O;
            N.anyOf = Q.options.map((w, B) => this.process(w, { ...K, path: [...K.path, "anyOf", B] }));
            break;
          }
          case "intersection": {
            let N = O, w = this.process(Q.left, { ...K, path: [...K.path, "allOf", 0] }), B = this.process(Q.right, { ...K, path: [...K.path, "allOf", 1] }), L = (I) => "allOf" in I && Object.keys(I).length === 1, j = [...L(w) ? w.allOf : [w], ...L(B) ? B.allOf : [B]];
            N.allOf = j;
            break;
          }
          case "tuple": {
            let N = O;
            N.type = "array";
            let w = Q.items.map((j, I) => this.process(j, { ...K, path: [...K.path, "prefixItems", I] }));
            if (this.target === "draft-2020-12") N.prefixItems = w;
            else N.items = w;
            if (Q.rest) {
              let j = this.process(Q.rest, { ...K, path: [...K.path, "items"] });
              if (this.target === "draft-2020-12") N.items = j;
              else N.additionalItems = j;
            }
            if (Q.rest) N.items = this.process(Q.rest, { ...K, path: [...K.path, "items"] });
            let { minimum: B, maximum: L } = $._zod.bag;
            if (typeof B === "number") N.minItems = B;
            if (typeof L === "number") N.maxItems = L;
            break;
          }
          case "record": {
            let N = O;
            N.type = "object", N.propertyNames = this.process(Q.keyType, { ...K, path: [...K.path, "propertyNames"] }), N.additionalProperties = this.process(Q.valueType, { ...K, path: [...K.path, "additionalProperties"] });
            break;
          }
          case "map": {
            if (this.unrepresentable === "throw") throw Error("Map cannot be represented in JSON Schema");
            break;
          }
          case "set": {
            if (this.unrepresentable === "throw") throw Error("Set cannot be represented in JSON Schema");
            break;
          }
          case "enum": {
            let N = O, w = K8(Q.entries);
            if (w.every((B) => typeof B === "number")) N.type = "number";
            if (w.every((B) => typeof B === "string")) N.type = "string";
            N.enum = w;
            break;
          }
          case "literal": {
            let N = O, w = [];
            for (let B of Q.values) if (B === void 0) {
              if (this.unrepresentable === "throw") throw Error("Literal `undefined` cannot be represented in JSON Schema");
            } else if (typeof B === "bigint") if (this.unrepresentable === "throw") throw Error("BigInt literals cannot be represented in JSON Schema");
            else w.push(Number(B));
            else w.push(B);
            if (w.length === 0) ;
            else if (w.length === 1) {
              let B = w[0];
              N.type = B === null ? "null" : typeof B, N.const = B;
            } else {
              if (w.every((B) => typeof B === "number")) N.type = "number";
              if (w.every((B) => typeof B === "string")) N.type = "string";
              if (w.every((B) => typeof B === "boolean")) N.type = "string";
              if (w.every((B) => B === null)) N.type = "null";
              N.enum = w;
            }
            break;
          }
          case "file": {
            let N = O, w = { type: "string", format: "binary", contentEncoding: "binary" }, { minimum: B, maximum: L, mime: j } = $._zod.bag;
            if (B !== void 0) w.minLength = B;
            if (L !== void 0) w.maxLength = L;
            if (j) if (j.length === 1) w.contentMediaType = j[0], Object.assign(N, w);
            else N.anyOf = j.map((I) => {
              return { ...w, contentMediaType: I };
            });
            else Object.assign(N, w);
            break;
          }
          case "transform": {
            if (this.unrepresentable === "throw") throw Error("Transforms cannot be represented in JSON Schema");
            break;
          }
          case "nullable": {
            let N = this.process(Q.innerType, K);
            O.anyOf = [N, { type: "null" }];
            break;
          }
          case "nonoptional": {
            this.process(Q.innerType, K), W.ref = Q.innerType;
            break;
          }
          case "success": {
            let N = O;
            N.type = "boolean";
            break;
          }
          case "default": {
            this.process(Q.innerType, K), W.ref = Q.innerType, O.default = JSON.parse(JSON.stringify(Q.defaultValue));
            break;
          }
          case "prefault": {
            if (this.process(Q.innerType, K), W.ref = Q.innerType, this.io === "input") O._prefault = JSON.parse(JSON.stringify(Q.defaultValue));
            break;
          }
          case "catch": {
            this.process(Q.innerType, K), W.ref = Q.innerType;
            let N;
            try {
              N = Q.catchValue(void 0);
            } catch {
              throw Error("Dynamic catch values are not supported in JSON Schema");
            }
            O.default = N;
            break;
          }
          case "nan": {
            if (this.unrepresentable === "throw") throw Error("NaN cannot be represented in JSON Schema");
            break;
          }
          case "template_literal": {
            let N = O, w = $._zod.pattern;
            if (!w) throw Error("Pattern not found in template literal");
            N.type = "string", N.pattern = w.source;
            break;
          }
          case "pipe": {
            let N = this.io === "input" ? Q.in._zod.def.type === "transform" ? Q.out : Q.in : Q.out;
            this.process(N, K), W.ref = N;
            break;
          }
          case "readonly": {
            this.process(Q.innerType, K), W.ref = Q.innerType, O.readOnly = true;
            break;
          }
          case "promise": {
            this.process(Q.innerType, K), W.ref = Q.innerType;
            break;
          }
          case "optional": {
            this.process(Q.innerType, K), W.ref = Q.innerType;
            break;
          }
          case "lazy": {
            let N = $._zod.innerType;
            this.process(N, K), W.ref = N;
            break;
          }
          case "custom": {
            if (this.unrepresentable === "throw") throw Error("Custom types cannot be represented in JSON Schema");
            break;
          }
          default:
        }
      }
    }
    let U = this.metadataRegistry.get($);
    if (U) Object.assign(W.schema, U);
    if (this.io === "input" && k$($)) delete W.schema.examples, delete W.schema.default;
    if (this.io === "input" && W.schema._prefault) (J = W.schema).default ?? (J.default = W.schema._prefault);
    return delete W.schema._prefault, this.seen.get($).schema;
  }
  emit($, X) {
    let J = { cycles: X?.cycles ?? "ref", reused: X?.reused ?? "inline", external: X?.external ?? void 0 }, Q = this.seen.get($);
    if (!Q) throw Error("Unprocessed schema. This is a bug in Zod.");
    let Y = (H) => {
      let K = this.target === "draft-2020-12" ? "$defs" : "definitions";
      if (J.external) {
        let w = J.external.registry.get(H[0])?.id;
        if (w) return { ref: J.external.uri(w) };
        let B = H[1].defId ?? H[1].schema.id ?? `schema${this.counter++}`;
        return H[1].defId = B, { defId: B, ref: `${J.external.uri("__shared")}#/${K}/${B}` };
      }
      if (H[1] === Q) return { ref: "#" };
      let O = `${"#"}/${K}/`, N = H[1].schema.id ?? `__schema${this.counter++}`;
      return { defId: N, ref: O + N };
    }, z = (H) => {
      if (H[1].schema.$ref) return;
      let K = H[1], { ref: V, defId: O } = Y(H);
      if (K.def = { ...K.schema }, O) K.defId = O;
      let N = K.schema;
      for (let w in N) delete N[w];
      N.$ref = V;
    };
    for (let H of this.seen.entries()) {
      let K = H[1];
      if ($ === H[0]) {
        z(H);
        continue;
      }
      if (J.external) {
        let O = J.external.registry.get(H[0])?.id;
        if ($ !== H[0] && O) {
          z(H);
          continue;
        }
      }
      if (this.metadataRegistry.get(H[0])?.id) {
        z(H);
        continue;
      }
      if (K.cycle) {
        if (J.cycles === "throw") throw Error(`Cycle detected: #/${K.cycle?.join("/")}/<root>

Set the \`cycles\` parameter to \`"ref"\` to resolve cyclical schemas with defs.`);
        else if (J.cycles === "ref") z(H);
        continue;
      }
      if (K.count > 1) {
        if (J.reused === "ref") {
          z(H);
          continue;
        }
      }
    }
    let W = (H, K) => {
      let V = this.seen.get(H), O = V.def ?? V.schema, N = { ...O };
      if (V.ref === null) return;
      let w = V.ref;
      if (V.ref = null, w) {
        W(w, K);
        let B = this.seen.get(w).schema;
        if (B.$ref && K.target === "draft-7") O.allOf = O.allOf ?? [], O.allOf.push(B);
        else Object.assign(O, B), Object.assign(O, N);
      }
      if (!V.isParent) this.override({ zodSchema: H, jsonSchema: O, path: V.path ?? [] });
    };
    for (let H of [...this.seen.entries()].reverse()) W(H[0], { target: this.target });
    let G = {};
    if (this.target === "draft-2020-12") G.$schema = "https://json-schema.org/draft/2020-12/schema";
    else if (this.target === "draft-7") G.$schema = "http://json-schema.org/draft-07/schema#";
    else console.warn(`Invalid target: ${this.target}`);
    Object.assign(G, Q.def);
    let U = J.external?.defs ?? {};
    for (let H of this.seen.entries()) {
      let K = H[1];
      if (K.def && K.defId) U[K.defId] = K.def;
    }
    if (!J.external && Object.keys(U).length > 0) if (this.target === "draft-2020-12") G.$defs = U;
    else G.definitions = U;
    try {
      return JSON.parse(JSON.stringify(G));
    } catch (H) {
      throw Error("Error converting schema to JSON.");
    }
  }
};
function Z0($, X) {
  if ($ instanceof I8) {
    let Q = new E7(X), Y = {};
    for (let G of $._idmap.entries()) {
      let [U, H] = G;
      Q.process(H);
    }
    let z = {}, W = { registry: $, uri: X?.uri || ((G) => G), defs: Y };
    for (let G of $._idmap.entries()) {
      let [U, H] = G;
      z[U] = Q.emit(H, { ...X, external: W });
    }
    if (Object.keys(Y).length > 0) {
      let G = Q.target === "draft-2020-12" ? "$defs" : "definitions";
      z.__shared = { [G]: Y };
    }
    return { schemas: z };
  }
  let J = new E7(X);
  return J.process($), J.emit($, X);
}
function k$($, X) {
  let J = X ?? { seen: /* @__PURE__ */ new Set() };
  if (J.seen.has($)) return false;
  J.seen.add($);
  let Y = $._zod.def;
  switch (Y.type) {
    case "string":
    case "number":
    case "bigint":
    case "boolean":
    case "date":
    case "symbol":
    case "undefined":
    case "null":
    case "any":
    case "unknown":
    case "never":
    case "void":
    case "literal":
    case "enum":
    case "nan":
    case "file":
    case "template_literal":
      return false;
    case "array":
      return k$(Y.element, J);
    case "object": {
      for (let z in Y.shape) if (k$(Y.shape[z], J)) return true;
      return false;
    }
    case "union": {
      for (let z of Y.options) if (k$(z, J)) return true;
      return false;
    }
    case "intersection":
      return k$(Y.left, J) || k$(Y.right, J);
    case "tuple": {
      for (let z of Y.items) if (k$(z, J)) return true;
      if (Y.rest && k$(Y.rest, J)) return true;
      return false;
    }
    case "record":
      return k$(Y.keyType, J) || k$(Y.valueType, J);
    case "map":
      return k$(Y.keyType, J) || k$(Y.valueType, J);
    case "set":
      return k$(Y.valueType, J);
    case "promise":
    case "optional":
    case "nonoptional":
    case "nullable":
    case "readonly":
      return k$(Y.innerType, J);
    case "lazy":
      return k$(Y.getter(), J);
    case "default":
      return k$(Y.innerType, J);
    case "prefault":
      return k$(Y.innerType, J);
    case "custom":
      return false;
    case "transform":
      return true;
    case "pipe":
      return k$(Y.in, J) || k$(Y.out, J);
    case "success":
      return false;
    case "catch":
      return false;
    default:
  }
  throw Error(`Unknown schema type: ${Y.type}`);
}
var RN = {};
var P2 = q("ZodMiniType", ($, X) => {
  if (!$._zod) throw Error("Uninitialized schema in ZodMiniType.");
  i.init($, X), $.def = X, $.parse = (J, Q) => j1($, J, Q, { callee: $.parse }), $.safeParse = (J, Q) => k4($, J, Q), $.parseAsync = async (J, Q) => F1($, J, Q, { callee: $.parseAsync }), $.safeParseAsync = async (J, Q) => _4($, J, Q), $.check = (...J) => {
    return $.clone({ ...X, checks: [...X.checks ?? [], ...J.map((Q) => typeof Q === "function" ? { _zod: { check: Q, def: { check: "custom" }, onattach: [] } } : Q)] });
  }, $.clone = (J, Q) => m$($, J, Q), $.brand = () => $, $.register = (J, Q) => {
    return J.add($, Q), $;
  };
});
var Z2 = q("ZodMiniObject", ($, X) => {
  j8.init($, X), P2.init($, X), E.defineLazy($, "shape", () => X.shape);
});
var u4 = {};
$1(u4, { xid: () => c2, void: () => Kb, uuidv7: () => y2, uuidv6: () => T2, uuidv4: () => x2, uuid: () => _2, url: () => f2, uppercase: () => r8, unknown: () => D$, union: () => U$, undefined: () => Ub, ulid: () => l2, uint64: () => Wb, uint32: () => Yb, tuple: () => wb, trim: () => $9, treeifyError: () => iJ, transform: () => nG, toUpperCase: () => J9, toLowerCase: () => X9, toJSONSchema: () => Z0, templateLiteral: () => Ab, symbol: () => Gb, superRefine: () => OV, success: () => Mb, stringbool: () => Zb, stringFormat: () => e2, string: () => F, strictObject: () => Ob, startsWith: () => t8, size: () => i8, setErrorMap: () => Sb, set: () => Db, safeParseAsync: () => IG, safeParse: () => MG, registry: () => A8, regexes: () => x4, regex: () => n8, refine: () => VV, record: () => w$, readonly: () => WV, property: () => NG, promise: () => bb, prettifyError: () => nJ, preprocess: () => c7, prefault: () => eN, positive: () => GG, pipe: () => f7, partialRecord: () => Bb, parseAsync: () => FG, parse: () => jG, overwrite: () => V4, optional: () => L$, object: () => _, number: () => G$, nullish: () => Fb, nullable: () => y7, null: () => H9, normalize: () => e8, nonpositive: () => HG, nonoptional: () => $V, nonnegative: () => KG, never: () => g7, negative: () => UG, nativeEnum: () => Lb, nanoid: () => h2, nan: () => Ib, multipleOf: () => b1, minSize: () => P1, minLength: () => f4, mime: () => s8, maxSize: () => A0, maxLength: () => b0, map: () => qb, lte: () => I6, lt: () => K4, lowercase: () => d8, looseObject: () => l$, locales: () => M0, literal: () => f, length: () => P0, lazy: () => HV, ksuid: () => p2, keyof: () => Vb, jwt: () => s2, json: () => Eb, iso: () => R0, ipv6: () => n2, ipv4: () => i2, intersection: () => K9, int64: () => zb, int32: () => Jb, int: () => AG, instanceof: () => Pb, includes: () => o8, guid: () => k2, gte: () => J6, gt: () => N4, globalRegistry: () => X6, getErrorMap: () => vb, function: () => Z7, formatError: () => B0, float64: () => Xb, float32: () => $b, flattenError: () => w0, file: () => jb, enum: () => d$, endsWith: () => a8, emoji: () => g2, email: () => C2, e164: () => a2, discriminatedUnion: () => m7, date: () => Nb, custom: () => tG, cuid2: () => m2, cuid: () => u2, core: () => C6, config: () => E$, coerce: () => aG, clone: () => m$, cidrv6: () => r2, cidrv4: () => d2, check: () => NV, catch: () => YV, boolean: () => S$, bigint: () => Qb, base64url: () => t2, base64: () => o2, array: () => $$, any: () => Hb, _default: () => aN, _ZodString: () => bG, ZodXID: () => kG, ZodVoid: () => uN, ZodUnknown: () => gN, ZodUnion: () => cG, ZodUndefined: () => TN, ZodUUID: () => O4, ZodURL: () => ZG, ZodULID: () => CG, ZodType: () => s, ZodTuple: () => pN, ZodTransform: () => iG, ZodTemplateLiteral: () => GV, ZodSymbol: () => xN, ZodSuccess: () => XV, ZodStringFormat: () => O$, ZodString: () => z9, ZodSet: () => nN, ZodRecord: () => pG, ZodRealError: () => S0, ZodReadonly: () => zV, ZodPromise: () => KV, ZodPrefault: () => sN, ZodPipe: () => oG, ZodOptional: () => dG, ZodObject: () => u7, ZodNumberFormat: () => v0, ZodNumber: () => W9, ZodNullable: () => oN, ZodNull: () => yN, ZodNonOptional: () => rG, ZodNever: () => hN, ZodNanoID: () => RG, ZodNaN: () => QV, ZodMap: () => iN, ZodLiteral: () => dN, ZodLazy: () => UV, ZodKSUID: () => _G, ZodJWT: () => mG, ZodIssueCode: () => Rb, ZodIntersection: () => cN, ZodISOTime: () => _7, ZodISODuration: () => x7, ZodISODateTime: () => C7, ZodISODate: () => k7, ZodIPv6: () => TG, ZodIPv4: () => xG, ZodGUID: () => T7, ZodFile: () => rN, ZodError: () => S2, ZodEnum: () => Q9, ZodEmoji: () => EG, ZodEmail: () => PG, ZodE164: () => uG, ZodDiscriminatedUnion: () => lN, ZodDefault: () => tN, ZodDate: () => h7, ZodCustomStringFormat: () => _N, ZodCustom: () => l7, ZodCatch: () => JV, ZodCUID2: () => vG, ZodCUID: () => SG, ZodCIDRv6: () => fG, ZodCIDRv4: () => yG, ZodBoolean: () => G9, ZodBigIntFormat: () => lG, ZodBigInt: () => U9, ZodBase64URL: () => hG, ZodBase64: () => gG, ZodArray: () => mN, ZodAny: () => fN, TimePrecision: () => J7, NEVER: () => lJ, $output: () => eY, $input: () => $7, $brand: () => cJ });
var R0 = {};
$1(R0, { time: () => DG, duration: () => LG, datetime: () => BG, date: () => qG, ZodISOTime: () => _7, ZodISODuration: () => x7, ZodISODateTime: () => C7, ZodISODate: () => k7 });
var C7 = q("ZodISODateTime", ($, X) => {
  HW.init($, X), O$.init($, X);
});
function BG($) {
  return eW(C7, $);
}
var k7 = q("ZodISODate", ($, X) => {
  KW.init($, X), O$.init($, X);
});
function qG($) {
  return $G(k7, $);
}
var _7 = q("ZodISOTime", ($, X) => {
  NW.init($, X), O$.init($, X);
});
function DG($) {
  return XG(_7, $);
}
var x7 = q("ZodISODuration", ($, X) => {
  VW.init($, X), O$.init($, X);
});
function LG($) {
  return JG(x7, $);
}
var kN = ($, X) => {
  q8.init($, X), $.name = "ZodError", Object.defineProperties($, { format: { value: (J) => B0($, J) }, flatten: { value: (J) => w0($, J) }, addIssue: { value: (J) => $.issues.push(J) }, addIssues: { value: (J) => $.issues.push(...J) }, isEmpty: { get() {
    return $.issues.length === 0;
  } } });
};
var S2 = q("ZodError", kN);
var S0 = q("ZodError", kN, { Parent: Error });
var jG = dJ(S0);
var FG = rJ(S0);
var MG = oJ(S0);
var IG = tJ(S0);
var s = q("ZodType", ($, X) => {
  return i.init($, X), $.def = X, Object.defineProperty($, "_def", { value: X }), $.check = (...J) => {
    return $.clone({ ...X, checks: [...X.checks ?? [], ...J.map((Q) => typeof Q === "function" ? { _zod: { check: Q, def: { check: "custom" }, onattach: [] } } : Q)] });
  }, $.clone = (J, Q) => m$($, J, Q), $.brand = () => $, $.register = (J, Q) => {
    return J.add($, Q), $;
  }, $.parse = (J, Q) => jG($, J, Q, { callee: $.parse }), $.safeParse = (J, Q) => MG($, J, Q), $.parseAsync = async (J, Q) => FG($, J, Q, { callee: $.parseAsync }), $.safeParseAsync = async (J, Q) => IG($, J, Q), $.spa = $.safeParseAsync, $.refine = (J, Q) => $.check(VV(J, Q)), $.superRefine = (J) => $.check(OV(J)), $.overwrite = (J) => $.check(V4(J)), $.optional = () => L$($), $.nullable = () => y7($), $.nullish = () => L$(y7($)), $.nonoptional = (J) => $V($, J), $.array = () => $$($), $.or = (J) => U$([$, J]), $.and = (J) => K9($, J), $.transform = (J) => f7($, nG(J)), $.default = (J) => aN($, J), $.prefault = (J) => eN($, J), $.catch = (J) => YV($, J), $.pipe = (J) => f7($, J), $.readonly = () => WV($), $.describe = (J) => {
    let Q = $.clone();
    return X6.add(Q, { description: J }), Q;
  }, Object.defineProperty($, "description", { get() {
    return X6.get($)?.description;
  }, configurable: true }), $.meta = (...J) => {
    if (J.length === 0) return X6.get($);
    let Q = $.clone();
    return X6.add(Q, J[0]), Q;
  }, $.isOptional = () => $.safeParse(void 0).success, $.isNullable = () => $.safeParse(null).success, $;
});
var bG = q("_ZodString", ($, X) => {
  T4.init($, X), s.init($, X);
  let J = $._zod.bag;
  $.format = J.format ?? null, $.minLength = J.minimum ?? null, $.maxLength = J.maximum ?? null, $.regex = (...Q) => $.check(n8(...Q)), $.includes = (...Q) => $.check(o8(...Q)), $.startsWith = (...Q) => $.check(t8(...Q)), $.endsWith = (...Q) => $.check(a8(...Q)), $.min = (...Q) => $.check(f4(...Q)), $.max = (...Q) => $.check(b0(...Q)), $.length = (...Q) => $.check(P0(...Q)), $.nonempty = (...Q) => $.check(f4(1, ...Q)), $.lowercase = (Q) => $.check(d8(Q)), $.uppercase = (Q) => $.check(r8(Q)), $.trim = () => $.check($9()), $.normalize = (...Q) => $.check(e8(...Q)), $.toLowerCase = () => $.check(X9()), $.toUpperCase = () => $.check(J9());
});
var z9 = q("ZodString", ($, X) => {
  T4.init($, X), bG.init($, X), $.email = (J) => $.check(b8(PG, J)), $.url = (J) => $.check(S8(ZG, J)), $.jwt = (J) => $.check(p8(mG, J)), $.emoji = (J) => $.check(v8(EG, J)), $.guid = (J) => $.check(I0(T7, J)), $.uuid = (J) => $.check(P8(O4, J)), $.uuidv4 = (J) => $.check(Z8(O4, J)), $.uuidv6 = (J) => $.check(E8(O4, J)), $.uuidv7 = (J) => $.check(R8(O4, J)), $.nanoid = (J) => $.check(C8(RG, J)), $.guid = (J) => $.check(I0(T7, J)), $.cuid = (J) => $.check(k8(SG, J)), $.cuid2 = (J) => $.check(_8(vG, J)), $.ulid = (J) => $.check(x8(CG, J)), $.base64 = (J) => $.check(m8(gG, J)), $.base64url = (J) => $.check(l8(hG, J)), $.xid = (J) => $.check(T8(kG, J)), $.ksuid = (J) => $.check(y8(_G, J)), $.ipv4 = (J) => $.check(f8(xG, J)), $.ipv6 = (J) => $.check(g8(TG, J)), $.cidrv4 = (J) => $.check(h8(yG, J)), $.cidrv6 = (J) => $.check(u8(fG, J)), $.e164 = (J) => $.check(c8(uG, J)), $.datetime = (J) => $.check(BG(J)), $.date = (J) => $.check(qG(J)), $.time = (J) => $.check(DG(J)), $.duration = (J) => $.check(LG(J));
});
function F($) {
  return X7(z9, $);
}
var O$ = q("ZodStringFormat", ($, X) => {
  H$.init($, X), bG.init($, X);
});
var PG = q("ZodEmail", ($, X) => {
  zY.init($, X), O$.init($, X);
});
function C2($) {
  return b8(PG, $);
}
var T7 = q("ZodGUID", ($, X) => {
  YY.init($, X), O$.init($, X);
});
function k2($) {
  return I0(T7, $);
}
var O4 = q("ZodUUID", ($, X) => {
  QY.init($, X), O$.init($, X);
});
function _2($) {
  return P8(O4, $);
}
function x2($) {
  return Z8(O4, $);
}
function T2($) {
  return E8(O4, $);
}
function y2($) {
  return R8(O4, $);
}
var ZG = q("ZodURL", ($, X) => {
  WY.init($, X), O$.init($, X);
});
function f2($) {
  return S8(ZG, $);
}
var EG = q("ZodEmoji", ($, X) => {
  GY.init($, X), O$.init($, X);
});
function g2($) {
  return v8(EG, $);
}
var RG = q("ZodNanoID", ($, X) => {
  UY.init($, X), O$.init($, X);
});
function h2($) {
  return C8(RG, $);
}
var SG = q("ZodCUID", ($, X) => {
  HY.init($, X), O$.init($, X);
});
function u2($) {
  return k8(SG, $);
}
var vG = q("ZodCUID2", ($, X) => {
  KY.init($, X), O$.init($, X);
});
function m2($) {
  return _8(vG, $);
}
var CG = q("ZodULID", ($, X) => {
  NY.init($, X), O$.init($, X);
});
function l2($) {
  return x8(CG, $);
}
var kG = q("ZodXID", ($, X) => {
  VY.init($, X), O$.init($, X);
});
function c2($) {
  return T8(kG, $);
}
var _G = q("ZodKSUID", ($, X) => {
  OY.init($, X), O$.init($, X);
});
function p2($) {
  return y8(_G, $);
}
var xG = q("ZodIPv4", ($, X) => {
  wY.init($, X), O$.init($, X);
});
function i2($) {
  return f8(xG, $);
}
var TG = q("ZodIPv6", ($, X) => {
  BY.init($, X), O$.init($, X);
});
function n2($) {
  return g8(TG, $);
}
var yG = q("ZodCIDRv4", ($, X) => {
  qY.init($, X), O$.init($, X);
});
function d2($) {
  return h8(yG, $);
}
var fG = q("ZodCIDRv6", ($, X) => {
  DY.init($, X), O$.init($, X);
});
function r2($) {
  return u8(fG, $);
}
var gG = q("ZodBase64", ($, X) => {
  LY.init($, X), O$.init($, X);
});
function o2($) {
  return m8(gG, $);
}
var hG = q("ZodBase64URL", ($, X) => {
  jY.init($, X), O$.init($, X);
});
function t2($) {
  return l8(hG, $);
}
var uG = q("ZodE164", ($, X) => {
  FY.init($, X), O$.init($, X);
});
function a2($) {
  return c8(uG, $);
}
var mG = q("ZodJWT", ($, X) => {
  MY.init($, X), O$.init($, X);
});
function s2($) {
  return p8(mG, $);
}
var _N = q("ZodCustomStringFormat", ($, X) => {
  IY.init($, X), O$.init($, X);
});
function e2($, X, J = {}) {
  return P7(_N, $, X, J);
}
var W9 = q("ZodNumber", ($, X) => {
  D8.init($, X), s.init($, X), $.gt = (Q, Y) => $.check(N4(Q, Y)), $.gte = (Q, Y) => $.check(J6(Q, Y)), $.min = (Q, Y) => $.check(J6(Q, Y)), $.lt = (Q, Y) => $.check(K4(Q, Y)), $.lte = (Q, Y) => $.check(I6(Q, Y)), $.max = (Q, Y) => $.check(I6(Q, Y)), $.int = (Q) => $.check(AG(Q)), $.safe = (Q) => $.check(AG(Q)), $.positive = (Q) => $.check(N4(0, Q)), $.nonnegative = (Q) => $.check(J6(0, Q)), $.negative = (Q) => $.check(K4(0, Q)), $.nonpositive = (Q) => $.check(I6(0, Q)), $.multipleOf = (Q, Y) => $.check(b1(Q, Y)), $.step = (Q, Y) => $.check(b1(Q, Y)), $.finite = () => $;
  let J = $._zod.bag;
  $.minValue = Math.max(J.minimum ?? Number.NEGATIVE_INFINITY, J.exclusiveMinimum ?? Number.NEGATIVE_INFINITY) ?? null, $.maxValue = Math.min(J.maximum ?? Number.POSITIVE_INFINITY, J.exclusiveMaximum ?? Number.POSITIVE_INFINITY) ?? null, $.isInt = (J.format ?? "").includes("int") || Number.isSafeInteger(J.multipleOf ?? 0.5), $.isFinite = true, $.format = J.format ?? null;
});
function G$($) {
  return Y7(W9, $);
}
var v0 = q("ZodNumberFormat", ($, X) => {
  AY.init($, X), W9.init($, X);
});
function AG($) {
  return Q7(v0, $);
}
function $b($) {
  return z7(v0, $);
}
function Xb($) {
  return W7(v0, $);
}
function Jb($) {
  return G7(v0, $);
}
function Yb($) {
  return U7(v0, $);
}
var G9 = q("ZodBoolean", ($, X) => {
  D0.init($, X), s.init($, X);
});
function S$($) {
  return H7(G9, $);
}
var U9 = q("ZodBigInt", ($, X) => {
  L8.init($, X), s.init($, X), $.gte = (Q, Y) => $.check(J6(Q, Y)), $.min = (Q, Y) => $.check(J6(Q, Y)), $.gt = (Q, Y) => $.check(N4(Q, Y)), $.gte = (Q, Y) => $.check(J6(Q, Y)), $.min = (Q, Y) => $.check(J6(Q, Y)), $.lt = (Q, Y) => $.check(K4(Q, Y)), $.lte = (Q, Y) => $.check(I6(Q, Y)), $.max = (Q, Y) => $.check(I6(Q, Y)), $.positive = (Q) => $.check(N4(BigInt(0), Q)), $.negative = (Q) => $.check(K4(BigInt(0), Q)), $.nonpositive = (Q) => $.check(I6(BigInt(0), Q)), $.nonnegative = (Q) => $.check(J6(BigInt(0), Q)), $.multipleOf = (Q, Y) => $.check(b1(Q, Y));
  let J = $._zod.bag;
  $.minValue = J.minimum ?? null, $.maxValue = J.maximum ?? null, $.format = J.format ?? null;
});
function Qb($) {
  return K7(U9, $);
}
var lG = q("ZodBigIntFormat", ($, X) => {
  bY.init($, X), U9.init($, X);
});
function zb($) {
  return N7(lG, $);
}
function Wb($) {
  return V7(lG, $);
}
var xN = q("ZodSymbol", ($, X) => {
  PY.init($, X), s.init($, X);
});
function Gb($) {
  return O7(xN, $);
}
var TN = q("ZodUndefined", ($, X) => {
  ZY.init($, X), s.init($, X);
});
function Ub($) {
  return w7(TN, $);
}
var yN = q("ZodNull", ($, X) => {
  EY.init($, X), s.init($, X);
});
function H9($) {
  return B7(yN, $);
}
var fN = q("ZodAny", ($, X) => {
  RY.init($, X), s.init($, X);
});
function Hb() {
  return q7(fN);
}
var gN = q("ZodUnknown", ($, X) => {
  I1.init($, X), s.init($, X);
});
function D$() {
  return A1(gN);
}
var hN = q("ZodNever", ($, X) => {
  SY.init($, X), s.init($, X);
});
function g7($) {
  return D7(hN, $);
}
var uN = q("ZodVoid", ($, X) => {
  vY.init($, X), s.init($, X);
});
function Kb($) {
  return L7(uN, $);
}
var h7 = q("ZodDate", ($, X) => {
  CY.init($, X), s.init($, X), $.min = (Q, Y) => $.check(J6(Q, Y)), $.max = (Q, Y) => $.check(I6(Q, Y));
  let J = $._zod.bag;
  $.minDate = J.minimum ? new Date(J.minimum) : null, $.maxDate = J.maximum ? new Date(J.maximum) : null;
});
function Nb($) {
  return j7(h7, $);
}
var mN = q("ZodArray", ($, X) => {
  L0.init($, X), s.init($, X), $.element = X.element, $.min = (J, Q) => $.check(f4(J, Q)), $.nonempty = (J) => $.check(f4(1, J)), $.max = (J, Q) => $.check(b0(J, Q)), $.length = (J, Q) => $.check(P0(J, Q)), $.unwrap = () => $.element;
});
function $$($, X) {
  return Y9(mN, $, X);
}
function Vb($) {
  let X = $._zod.def.shape;
  return f(Object.keys(X));
}
var u7 = q("ZodObject", ($, X) => {
  j8.init($, X), s.init($, X), E.defineLazy($, "shape", () => X.shape), $.keyof = () => d$(Object.keys($._zod.def.shape)), $.catchall = (J) => $.clone({ ...$._zod.def, catchall: J }), $.passthrough = () => $.clone({ ...$._zod.def, catchall: D$() }), $.loose = () => $.clone({ ...$._zod.def, catchall: D$() }), $.strict = () => $.clone({ ...$._zod.def, catchall: g7() }), $.strip = () => $.clone({ ...$._zod.def, catchall: void 0 }), $.extend = (J) => {
    return E.extend($, J);
  }, $.merge = (J) => E.merge($, J), $.pick = (J) => E.pick($, J), $.omit = (J) => E.omit($, J), $.partial = (...J) => E.partial(dG, $, J[0]), $.required = (...J) => E.required(rG, $, J[0]);
});
function _($, X) {
  let J = { type: "object", get shape() {
    return E.assignProp(this, "shape", { ...$ }), this.shape;
  }, ...E.normalizeParams(X) };
  return new u7(J);
}
function Ob($, X) {
  return new u7({ type: "object", get shape() {
    return E.assignProp(this, "shape", { ...$ }), this.shape;
  }, catchall: g7(), ...E.normalizeParams(X) });
}
function l$($, X) {
  return new u7({ type: "object", get shape() {
    return E.assignProp(this, "shape", { ...$ }), this.shape;
  }, catchall: D$(), ...E.normalizeParams(X) });
}
var cG = q("ZodUnion", ($, X) => {
  F8.init($, X), s.init($, X), $.options = X.options;
});
function U$($, X) {
  return new cG({ type: "union", options: $, ...E.normalizeParams(X) });
}
var lN = q("ZodDiscriminatedUnion", ($, X) => {
  cG.init($, X), kY.init($, X);
});
function m7($, X, J) {
  return new lN({ type: "union", options: X, discriminator: $, ...E.normalizeParams(J) });
}
var cN = q("ZodIntersection", ($, X) => {
  _Y.init($, X), s.init($, X);
});
function K9($, X) {
  return new cN({ type: "intersection", left: $, right: X });
}
var pN = q("ZodTuple", ($, X) => {
  y4.init($, X), s.init($, X), $.rest = (J) => $.clone({ ...$._zod.def, rest: J });
});
function wb($, X, J) {
  let Q = X instanceof i, Y = Q ? J : X;
  return new pN({ type: "tuple", items: $, rest: Q ? X : null, ...E.normalizeParams(Y) });
}
var pG = q("ZodRecord", ($, X) => {
  xY.init($, X), s.init($, X), $.keyType = X.keyType, $.valueType = X.valueType;
});
function w$($, X, J) {
  return new pG({ type: "record", keyType: $, valueType: X, ...E.normalizeParams(J) });
}
function Bb($, X, J) {
  return new pG({ type: "record", keyType: U$([$, g7()]), valueType: X, ...E.normalizeParams(J) });
}
var iN = q("ZodMap", ($, X) => {
  TY.init($, X), s.init($, X), $.keyType = X.keyType, $.valueType = X.valueType;
});
function qb($, X, J) {
  return new iN({ type: "map", keyType: $, valueType: X, ...E.normalizeParams(J) });
}
var nN = q("ZodSet", ($, X) => {
  yY.init($, X), s.init($, X), $.min = (...J) => $.check(P1(...J)), $.nonempty = (J) => $.check(P1(1, J)), $.max = (...J) => $.check(A0(...J)), $.size = (...J) => $.check(i8(...J));
});
function Db($, X) {
  return new nN({ type: "set", valueType: $, ...E.normalizeParams(X) });
}
var Q9 = q("ZodEnum", ($, X) => {
  fY.init($, X), s.init($, X), $.enum = X.entries, $.options = Object.values(X.entries);
  let J = new Set(Object.keys(X.entries));
  $.extract = (Q, Y) => {
    let z = {};
    for (let W of Q) if (J.has(W)) z[W] = X.entries[W];
    else throw Error(`Key ${W} not found in enum`);
    return new Q9({ ...X, checks: [], ...E.normalizeParams(Y), entries: z });
  }, $.exclude = (Q, Y) => {
    let z = { ...X.entries };
    for (let W of Q) if (J.has(W)) delete z[W];
    else throw Error(`Key ${W} not found in enum`);
    return new Q9({ ...X, checks: [], ...E.normalizeParams(Y), entries: z });
  };
});
function d$($, X) {
  let J = Array.isArray($) ? Object.fromEntries($.map((Q) => [Q, Q])) : $;
  return new Q9({ type: "enum", entries: J, ...E.normalizeParams(X) });
}
function Lb($, X) {
  return new Q9({ type: "enum", entries: $, ...E.normalizeParams(X) });
}
var dN = q("ZodLiteral", ($, X) => {
  gY.init($, X), s.init($, X), $.values = new Set(X.values), Object.defineProperty($, "value", { get() {
    if (X.values.length > 1) throw Error("This schema contains multiple valid literal values. Use `.values` instead.");
    return X.values[0];
  } });
});
function f($, X) {
  return new dN({ type: "literal", values: Array.isArray($) ? $ : [$], ...E.normalizeParams(X) });
}
var rN = q("ZodFile", ($, X) => {
  hY.init($, X), s.init($, X), $.min = (J, Q) => $.check(P1(J, Q)), $.max = (J, Q) => $.check(A0(J, Q)), $.mime = (J, Q) => $.check(s8(Array.isArray(J) ? J : [J], Q));
});
function jb($) {
  return M7(rN, $);
}
var iG = q("ZodTransform", ($, X) => {
  j0.init($, X), s.init($, X), $._zod.parse = (J, Q) => {
    J.addIssue = (z) => {
      if (typeof z === "string") J.issues.push(E.issue(z, J.value, X));
      else {
        let W = z;
        if (W.fatal) W.continue = false;
        W.code ?? (W.code = "custom"), W.input ?? (W.input = J.value), W.inst ?? (W.inst = $), W.continue ?? (W.continue = true), J.issues.push(E.issue(W));
      }
    };
    let Y = X.transform(J.value, J);
    if (Y instanceof Promise) return Y.then((z) => {
      return J.value = z, J;
    });
    return J.value = Y, J;
  };
});
function nG($) {
  return new iG({ type: "transform", transform: $ });
}
var dG = q("ZodOptional", ($, X) => {
  uY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function L$($) {
  return new dG({ type: "optional", innerType: $ });
}
var oN = q("ZodNullable", ($, X) => {
  mY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function y7($) {
  return new oN({ type: "nullable", innerType: $ });
}
function Fb($) {
  return L$(y7($));
}
var tN = q("ZodDefault", ($, X) => {
  lY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType, $.removeDefault = $.unwrap;
});
function aN($, X) {
  return new tN({ type: "default", innerType: $, get defaultValue() {
    return typeof X === "function" ? X() : X;
  } });
}
var sN = q("ZodPrefault", ($, X) => {
  cY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function eN($, X) {
  return new sN({ type: "prefault", innerType: $, get defaultValue() {
    return typeof X === "function" ? X() : X;
  } });
}
var rG = q("ZodNonOptional", ($, X) => {
  pY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function $V($, X) {
  return new rG({ type: "nonoptional", innerType: $, ...E.normalizeParams(X) });
}
var XV = q("ZodSuccess", ($, X) => {
  iY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function Mb($) {
  return new XV({ type: "success", innerType: $ });
}
var JV = q("ZodCatch", ($, X) => {
  nY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType, $.removeCatch = $.unwrap;
});
function YV($, X) {
  return new JV({ type: "catch", innerType: $, catchValue: typeof X === "function" ? X : () => X });
}
var QV = q("ZodNaN", ($, X) => {
  dY.init($, X), s.init($, X);
});
function Ib($) {
  return F7(QV, $);
}
var oG = q("ZodPipe", ($, X) => {
  F0.init($, X), s.init($, X), $.in = X.in, $.out = X.out;
});
function f7($, X) {
  return new oG({ type: "pipe", in: $, out: X });
}
var zV = q("ZodReadonly", ($, X) => {
  rY.init($, X), s.init($, X);
});
function WV($) {
  return new zV({ type: "readonly", innerType: $ });
}
var GV = q("ZodTemplateLiteral", ($, X) => {
  oY.init($, X), s.init($, X);
});
function Ab($, X) {
  return new GV({ type: "template_literal", parts: $, ...E.normalizeParams(X) });
}
var UV = q("ZodLazy", ($, X) => {
  aY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.getter();
});
function HV($) {
  return new UV({ type: "lazy", getter: $ });
}
var KV = q("ZodPromise", ($, X) => {
  tY.init($, X), s.init($, X), $.unwrap = () => $._zod.def.innerType;
});
function bb($) {
  return new KV({ type: "promise", innerType: $ });
}
var l7 = q("ZodCustom", ($, X) => {
  sY.init($, X), s.init($, X);
});
function NV($, X) {
  let J = new M$({ check: "custom", ...E.normalizeParams(X) });
  return J._zod.check = $, J;
}
function tG($, X) {
  return I7(l7, $ ?? (() => true), X);
}
function VV($, X = {}) {
  return A7(l7, $, X);
}
function OV($, X) {
  let J = NV((Q) => {
    return Q.addIssue = (Y) => {
      if (typeof Y === "string") Q.issues.push(E.issue(Y, Q.value, J._zod.def));
      else {
        let z = Y;
        if (z.fatal) z.continue = false;
        z.code ?? (z.code = "custom"), z.input ?? (z.input = Q.value), z.inst ?? (z.inst = J), z.continue ?? (z.continue = !J._zod.def.abort), Q.issues.push(E.issue(z));
      }
    }, $(Q.value, Q);
  }, X);
  return J;
}
function Pb($, X = { error: `Input not instance of ${$.name}` }) {
  let J = new l7({ type: "custom", check: "custom", fn: (Q) => Q instanceof $, abort: true, ...E.normalizeParams(X) });
  return J._zod.bag.Class = $, J;
}
var Zb = (...$) => b7({ Pipe: oG, Boolean: G9, String: z9, Transform: iG }, ...$);
function Eb($) {
  let X = HV(() => {
    return U$([F($), G$(), S$(), H9(), $$(X), w$(F(), X)]);
  });
  return X;
}
function c7($, X) {
  return f7(nG($), X);
}
var Rb = { invalid_type: "invalid_type", too_big: "too_big", too_small: "too_small", invalid_format: "invalid_format", not_multiple_of: "not_multiple_of", unrecognized_keys: "unrecognized_keys", invalid_union: "invalid_union", invalid_key: "invalid_key", invalid_element: "invalid_element", invalid_value: "invalid_value", custom: "custom" };
function Sb($) {
  E$({ customError: $ });
}
function vb() {
  return E$().customError;
}
var aG = {};
$1(aG, { string: () => Cb, number: () => kb, date: () => Tb, boolean: () => _b, bigint: () => xb });
function Cb($) {
  return sW(z9, $);
}
function kb($) {
  return YG(W9, $);
}
function _b($) {
  return QG(G9, $);
}
function xb($) {
  return zG(U9, $);
}
function Tb($) {
  return WG(h7, $);
}
E$(M8());
var m4 = "io.modelcontextprotocol/related-task";
var i7 = "2.0";
var y$ = tG(($) => $ !== null && (typeof $ === "object" || typeof $ === "function"));
var BV = U$([F(), G$().int()]);
var qV = F();
var Yn = l$({ ttl: U$([G$(), H9()]).optional(), pollInterval: G$().optional() });
var yb = _({ ttl: G$().optional() });
var fb = _({ taskId: F() });
var eG = l$({ progressToken: BV.optional(), [m4]: fb.optional() });
var w6 = _({ _meta: eG.optional() });
var N9 = w6.extend({ task: yb.optional() });
var f$ = _({ method: F(), params: w6.loose().optional() });
var b6 = _({ _meta: eG.optional() });
var P6 = _({ method: F(), params: b6.loose().optional() });
var g$ = l$({ _meta: eG.optional() });
var n7 = U$([F(), G$().int()]);
var LV = _({ jsonrpc: f(i7), id: n7, ...f$.shape }).strict();
var jV = _({ jsonrpc: f(i7), ...P6.shape }).strict();
var X3 = _({ jsonrpc: f(i7), id: n7, result: g$ }).strict();
var m;
(function($) {
  $[$.ConnectionClosed = -32e3] = "ConnectionClosed", $[$.RequestTimeout = -32001] = "RequestTimeout", $[$.ParseError = -32700] = "ParseError", $[$.InvalidRequest = -32600] = "InvalidRequest", $[$.MethodNotFound = -32601] = "MethodNotFound", $[$.InvalidParams = -32602] = "InvalidParams", $[$.InternalError = -32603] = "InternalError", $[$.UrlElicitationRequired = -32042] = "UrlElicitationRequired";
})(m || (m = {}));
var J3 = _({ jsonrpc: f(i7), id: n7.optional(), error: _({ code: G$().int(), message: F(), data: D$().optional() }) }).strict();
var Qn = U$([LV, jV, X3, J3]);
var zn = U$([X3, J3]);
var d7 = g$.strict();
var gb = b6.extend({ requestId: n7.optional(), reason: F().optional() });
var r7 = P6.extend({ method: f("notifications/cancelled"), params: gb });
var hb = _({ src: F(), mimeType: F().optional(), sizes: $$(F()).optional(), theme: d$(["light", "dark"]).optional() });
var O9 = _({ icons: $$(hb).optional() });
var C0 = _({ name: F(), title: F().optional() });
var IV = C0.extend({ ...C0.shape, ...O9.shape, version: F(), websiteUrl: F().optional(), description: F().optional() });
var ub = K9(_({ applyDefaults: S$().optional() }), w$(F(), D$()));
var mb = c7(($) => {
  if ($ && typeof $ === "object" && !Array.isArray($)) {
    if (Object.keys($).length === 0) return { form: {} };
  }
  return $;
}, K9(_({ form: ub.optional(), url: y$.optional() }), w$(F(), D$()).optional()));
var lb = l$({ list: y$.optional(), cancel: y$.optional(), requests: l$({ sampling: l$({ createMessage: y$.optional() }).optional(), elicitation: l$({ create: y$.optional() }).optional() }).optional() });
var cb = l$({ list: y$.optional(), cancel: y$.optional(), requests: l$({ tools: l$({ call: y$.optional() }).optional() }).optional() });
var pb = _({ experimental: w$(F(), y$).optional(), sampling: _({ context: y$.optional(), tools: y$.optional() }).optional(), elicitation: mb.optional(), roots: _({ listChanged: S$().optional() }).optional(), tasks: lb.optional() });
var ib = w6.extend({ protocolVersion: F(), capabilities: pb, clientInfo: IV });
var Y3 = f$.extend({ method: f("initialize"), params: ib });
var nb = _({ experimental: w$(F(), y$).optional(), logging: y$.optional(), completions: y$.optional(), prompts: _({ listChanged: S$().optional() }).optional(), resources: _({ subscribe: S$().optional(), listChanged: S$().optional() }).optional(), tools: _({ listChanged: S$().optional() }).optional(), tasks: cb.optional() });
var db = g$.extend({ protocolVersion: F(), capabilities: nb, serverInfo: IV, instructions: F().optional() });
var Q3 = P6.extend({ method: f("notifications/initialized"), params: b6.optional() });
var o7 = f$.extend({ method: f("ping"), params: w6.optional() });
var rb = _({ progress: G$(), total: L$(G$()), message: L$(F()) });
var ob = _({ ...b6.shape, ...rb.shape, progressToken: BV });
var t7 = P6.extend({ method: f("notifications/progress"), params: ob });
var tb = w6.extend({ cursor: qV.optional() });
var w9 = f$.extend({ params: tb.optional() });
var B9 = g$.extend({ nextCursor: qV.optional() });
var ab = d$(["working", "input_required", "completed", "failed", "cancelled"]);
var q9 = _({ taskId: F(), status: ab, ttl: U$([G$(), H9()]), createdAt: F(), lastUpdatedAt: F(), pollInterval: L$(G$()), statusMessage: L$(F()) });
var k0 = g$.extend({ task: q9 });
var sb = b6.merge(q9);
var D9 = P6.extend({ method: f("notifications/tasks/status"), params: sb });
var a7 = f$.extend({ method: f("tasks/get"), params: w6.extend({ taskId: F() }) });
var s7 = g$.merge(q9);
var e7 = f$.extend({ method: f("tasks/result"), params: w6.extend({ taskId: F() }) });
var Wn = g$.loose();
var $Q = w9.extend({ method: f("tasks/list") });
var XQ = B9.extend({ tasks: $$(q9) });
var JQ = f$.extend({ method: f("tasks/cancel"), params: w6.extend({ taskId: F() }) });
var AV = g$.merge(q9);
var bV = _({ uri: F(), mimeType: L$(F()), _meta: w$(F(), D$()).optional() });
var PV = bV.extend({ text: F() });
var z3 = F().refine(($) => {
  try {
    return atob($), true;
  } catch {
    return false;
  }
}, { message: "Invalid Base64 string" });
var ZV = bV.extend({ blob: z3 });
var L9 = d$(["user", "assistant"]);
var _0 = _({ audience: $$(L9).optional(), priority: G$().min(0).max(1).optional(), lastModified: R0.datetime({ offset: true }).optional() });
var EV = _({ ...C0.shape, ...O9.shape, uri: F(), description: L$(F()), mimeType: L$(F()), annotations: _0.optional(), _meta: L$(l$({})) });
var eb = _({ ...C0.shape, ...O9.shape, uriTemplate: F(), description: L$(F()), mimeType: L$(F()), annotations: _0.optional(), _meta: L$(l$({})) });
var YQ = w9.extend({ method: f("resources/list") });
var $P = B9.extend({ resources: $$(EV) });
var QQ = w9.extend({ method: f("resources/templates/list") });
var XP = B9.extend({ resourceTemplates: $$(eb) });
var W3 = w6.extend({ uri: F() });
var JP = W3;
var zQ = f$.extend({ method: f("resources/read"), params: JP });
var YP = g$.extend({ contents: $$(U$([PV, ZV])) });
var QP = P6.extend({ method: f("notifications/resources/list_changed"), params: b6.optional() });
var zP = W3;
var WP = f$.extend({ method: f("resources/subscribe"), params: zP });
var GP = W3;
var UP = f$.extend({ method: f("resources/unsubscribe"), params: GP });
var HP = b6.extend({ uri: F() });
var KP = P6.extend({ method: f("notifications/resources/updated"), params: HP });
var NP = _({ name: F(), description: L$(F()), required: L$(S$()) });
var VP = _({ ...C0.shape, ...O9.shape, description: L$(F()), arguments: L$($$(NP)), _meta: L$(l$({})) });
var WQ = w9.extend({ method: f("prompts/list") });
var OP = B9.extend({ prompts: $$(VP) });
var wP = w6.extend({ name: F(), arguments: w$(F(), F()).optional() });
var GQ = f$.extend({ method: f("prompts/get"), params: wP });
var G3 = _({ type: f("text"), text: F(), annotations: _0.optional(), _meta: w$(F(), D$()).optional() });
var U3 = _({ type: f("image"), data: z3, mimeType: F(), annotations: _0.optional(), _meta: w$(F(), D$()).optional() });
var H3 = _({ type: f("audio"), data: z3, mimeType: F(), annotations: _0.optional(), _meta: w$(F(), D$()).optional() });
var BP = _({ type: f("tool_use"), name: F(), id: F(), input: w$(F(), D$()), _meta: w$(F(), D$()).optional() });
var qP = _({ type: f("resource"), resource: U$([PV, ZV]), annotations: _0.optional(), _meta: w$(F(), D$()).optional() });
var DP = EV.extend({ type: f("resource_link") });
var K3 = U$([G3, U3, H3, DP, qP]);
var LP = _({ role: L9, content: K3 });
var jP = g$.extend({ description: F().optional(), messages: $$(LP) });
var FP = P6.extend({ method: f("notifications/prompts/list_changed"), params: b6.optional() });
var MP = _({ title: F().optional(), readOnlyHint: S$().optional(), destructiveHint: S$().optional(), idempotentHint: S$().optional(), openWorldHint: S$().optional() });
var IP = _({ taskSupport: d$(["required", "optional", "forbidden"]).optional() });
var RV = _({ ...C0.shape, ...O9.shape, description: F().optional(), inputSchema: _({ type: f("object"), properties: w$(F(), y$).optional(), required: $$(F()).optional() }).catchall(D$()), outputSchema: _({ type: f("object"), properties: w$(F(), y$).optional(), required: $$(F()).optional() }).catchall(D$()).optional(), annotations: MP.optional(), execution: IP.optional(), _meta: w$(F(), D$()).optional() });
var UQ = w9.extend({ method: f("tools/list") });
var AP = B9.extend({ tools: $$(RV) });
var HQ = g$.extend({ content: $$(K3).default([]), structuredContent: w$(F(), D$()).optional(), isError: S$().optional() });
var Gn = HQ.or(g$.extend({ toolResult: D$() }));
var bP = N9.extend({ name: F(), arguments: w$(F(), D$()).optional() });
var x0 = f$.extend({ method: f("tools/call"), params: bP });
var PP = P6.extend({ method: f("notifications/tools/list_changed"), params: b6.optional() });
var Un = _({ autoRefresh: S$().default(true), debounceMs: G$().int().nonnegative().default(300) });
var j9 = d$(["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"]);
var ZP = w6.extend({ level: j9 });
var N3 = f$.extend({ method: f("logging/setLevel"), params: ZP });
var EP = b6.extend({ level: j9, logger: F().optional(), data: D$() });
var RP = P6.extend({ method: f("notifications/message"), params: EP });
var SP = _({ name: F().optional() });
var vP = _({ hints: $$(SP).optional(), costPriority: G$().min(0).max(1).optional(), speedPriority: G$().min(0).max(1).optional(), intelligencePriority: G$().min(0).max(1).optional() });
var CP = _({ mode: d$(["auto", "required", "none"]).optional() });
var kP = _({ type: f("tool_result"), toolUseId: F().describe("The unique identifier for the corresponding tool call."), content: $$(K3).default([]), structuredContent: _({}).loose().optional(), isError: S$().optional(), _meta: w$(F(), D$()).optional() });
var _P = m7("type", [G3, U3, H3]);
var p7 = m7("type", [G3, U3, H3, BP, kP]);
var xP = _({ role: L9, content: U$([p7, $$(p7)]), _meta: w$(F(), D$()).optional() });
var TP = N9.extend({ messages: $$(xP), modelPreferences: vP.optional(), systemPrompt: F().optional(), includeContext: d$(["none", "thisServer", "allServers"]).optional(), temperature: G$().optional(), maxTokens: G$().int(), stopSequences: $$(F()).optional(), metadata: y$.optional(), tools: $$(RV).optional(), toolChoice: CP.optional() });
var yP = f$.extend({ method: f("sampling/createMessage"), params: TP });
var F9 = g$.extend({ model: F(), stopReason: L$(d$(["endTurn", "stopSequence", "maxTokens"]).or(F())), role: L9, content: _P });
var V3 = g$.extend({ model: F(), stopReason: L$(d$(["endTurn", "stopSequence", "maxTokens", "toolUse"]).or(F())), role: L9, content: U$([p7, $$(p7)]) });
var fP = _({ type: f("boolean"), title: F().optional(), description: F().optional(), default: S$().optional() });
var gP = _({ type: f("string"), title: F().optional(), description: F().optional(), minLength: G$().optional(), maxLength: G$().optional(), format: d$(["email", "uri", "date", "date-time"]).optional(), default: F().optional() });
var hP = _({ type: d$(["number", "integer"]), title: F().optional(), description: F().optional(), minimum: G$().optional(), maximum: G$().optional(), default: G$().optional() });
var uP = _({ type: f("string"), title: F().optional(), description: F().optional(), enum: $$(F()), default: F().optional() });
var mP = _({ type: f("string"), title: F().optional(), description: F().optional(), oneOf: $$(_({ const: F(), title: F() })), default: F().optional() });
var lP = _({ type: f("string"), title: F().optional(), description: F().optional(), enum: $$(F()), enumNames: $$(F()).optional(), default: F().optional() });
var cP = U$([uP, mP]);
var pP = _({ type: f("array"), title: F().optional(), description: F().optional(), minItems: G$().optional(), maxItems: G$().optional(), items: _({ type: f("string"), enum: $$(F()) }), default: $$(F()).optional() });
var iP = _({ type: f("array"), title: F().optional(), description: F().optional(), minItems: G$().optional(), maxItems: G$().optional(), items: _({ anyOf: $$(_({ const: F(), title: F() })) }), default: $$(F()).optional() });
var nP = U$([pP, iP]);
var dP = U$([lP, cP, nP]);
var rP = U$([dP, fP, gP, hP]);
var oP = N9.extend({ mode: f("form").optional(), message: F(), requestedSchema: _({ type: f("object"), properties: w$(F(), rP), required: $$(F()).optional() }) });
var tP = N9.extend({ mode: f("url"), message: F(), elicitationId: F(), url: F().url() });
var aP = U$([oP, tP]);
var sP = f$.extend({ method: f("elicitation/create"), params: aP });
var eP = b6.extend({ elicitationId: F() });
var $Z = P6.extend({ method: f("notifications/elicitation/complete"), params: eP });
var T0 = g$.extend({ action: d$(["accept", "decline", "cancel"]), content: c7(($) => $ === null ? void 0 : $, w$(F(), U$([F(), G$(), S$(), $$(F())])).optional()) });
var XZ = _({ type: f("ref/resource"), uri: F() });
var JZ = _({ type: f("ref/prompt"), name: F() });
var YZ = w6.extend({ ref: U$([JZ, XZ]), argument: _({ name: F(), value: F() }), context: _({ arguments: w$(F(), F()).optional() }).optional() });
var KQ = f$.extend({ method: f("completion/complete"), params: YZ });
var QZ = g$.extend({ completion: l$({ values: $$(F()).max(100), total: L$(G$().int()), hasMore: L$(S$()) }) });
var zZ = _({ uri: F().startsWith("file://"), name: F().optional(), _meta: w$(F(), D$()).optional() });
var WZ = f$.extend({ method: f("roots/list"), params: w6.optional() });
var O3 = g$.extend({ roots: $$(zZ) });
var GZ = P6.extend({ method: f("notifications/roots/list_changed"), params: b6.optional() });
var Hn = U$([o7, Y3, KQ, N3, GQ, WQ, YQ, QQ, zQ, WP, UP, x0, UQ, a7, e7, $Q, JQ]);
var Kn = U$([r7, t7, Q3, GZ, D9]);
var Nn = U$([d7, F9, V3, T0, O3, s7, XQ, k0]);
var Vn = U$([o7, yP, sP, WZ, a7, e7, $Q, JQ]);
var On = U$([r7, t7, RP, KP, QP, PP, FP, D9, $Z]);
var wn = U$([d7, db, QZ, jP, OP, $P, XP, YP, HQ, AP, s7, XQ, k0]);
var _V = Symbol("Let zodToJsonSchema decide on which parser to use");
var KZ = new Set("ABCDEFGHIJKLMNOPQRSTUVXYZabcdefghijklmnopqrstuvxyz0123456789");
var rD = uU(BU(), 1);
var oD = uU(dD(), 1);
var eD = Symbol.for("mcp.completable");
var sD;
(function($) {
  $.Completable = "McpCompletable";
})(sD || (sD = {}));
function QL($) {
  let X;
  return () => X ??= $();
}
var yx = QL(() => u4.object({ session_id: u4.string(), ws_url: u4.string(), work_dir: u4.string().optional(), session_key: u4.string().optional() }));
function HL($, X) {
  let { systemPrompt: J, settings: Q, settingSources: Y, sandbox: z, ...W } = $ ?? {}, G, U;
  if (J === void 0) G = "";
  else if (typeof J === "string") G = J;
  else if (J.type === "preset") U = J.append;
  let H = W.pathToClaudeCodeExecutable;
  if (!H) {
    let r9 = (0, import_url.fileURLToPath)(__loom_spawn_driver_meta_url), T1 = (0, import_path.join)(r9, "..");
    H = (0, import_path.join)(T1, "cli.js");
  }
  process.env.CLAUDE_AGENT_SDK_VERSION = "0.2.92";
  let { abortController: K = y1(), additionalDirectories: V = [], agent: O, agents: N, allowedTools: w = [], betas: B, canUseTool: L, continue: j, cwd: I, debug: b, debugFile: x, disallowedTools: h = [], tools: B$, env: x$, executable: G6 = f1() ? "bun" : "node", executableArgs: o6 = [], extraArgs: u6 = {}, fallbackModel: a4, enableFileCheckpointing: _1, toolConfig: t6, forkSession: r0, hooks: p, includeHookEvents: n9, includePartialMessages: aQ, onElicitation: o0, persistSession: t0, thinking: s4, effort: d9, maxThinkingTokens: x1, maxTurns: p$, maxBudgetUsd: j4, taskBudget: a0, mcpServers: _U, model: NL, outputFormat: xU, permissionMode: VL = "default", allowDangerouslySkipPermissions: OL = false, permissionPromptToolName: wL, plugins: BL, workload: TU, resume: qL, resumeSessionAt: DL, sessionId: LL, stderr: jL, strictMcpConfig: FL } = W, yU = xU?.type === "json_schema" ? xU.schema : void 0, e4 = x$;
  if (!e4) e4 = { ...process.env };
  if (!e4.CLAUDE_CODE_ENTRYPOINT) e4.CLAUDE_CODE_ENTRYPOINT = "sdk-ts";
  if (_1) e4.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = "true";
  if (t6?.askUserQuestion?.previewFormat) e4.CLAUDE_CODE_QUESTION_PREVIEW_FORMAT = t6.askUserQuestion.previewFormat;
  let fU = {}, gU = /* @__PURE__ */ new Map();
  if (_U) for (let [r9, T1] of Object.entries(_U)) if (T1.type === "sdk" && T1.instance) gU.set(r9, T1.instance);
  else fU[r9] = T1;
  let s0;
  if (s4) switch (s4.type) {
    case "adaptive":
      s0 = { type: "adaptive" };
      break;
    case "enabled":
      s0 = { type: "enabled", budgetTokens: s4.budgetTokens };
      break;
    case "disabled":
      s0 = { type: "disabled" };
      break;
  }
  else if (x1 !== void 0) s0 = x1 === 0 ? { type: "disabled" } : { type: "enabled", budgetTokens: x1 };
  let hU = new cX({ abortController: K, additionalDirectories: V, agent: O, betas: B, cwd: I, debug: b, debugFile: x, executable: G6, executableArgs: o6, extraArgs: TU ? { ...u6, workload: TU } : u6, pathToClaudeCodeExecutable: H, env: e4, forkSession: r0, stderr: jL, thinkingConfig: s0, effort: d9, maxTurns: p$, maxBudgetUsd: j4, taskBudget: a0, model: NL, fallbackModel: a4, jsonSchema: yU, permissionMode: VL, allowDangerouslySkipPermissions: OL, permissionPromptToolName: wL, continueConversation: j, resume: qL, resumeSessionAt: DL, sessionId: LL, settings: typeof Q === "object" ? q$(Q) : Q, settingSources: Y, allowedTools: w, disallowedTools: h, tools: B$, mcpServers: fU, strictMcpConfig: FL, canUseTool: !!L, hooks: !!p, includeHookEvents: n9, includePartialMessages: aQ, persistSession: t0, plugins: BL, sandbox: z, spawnClaudeCodeProcess: W.spawnClaudeCodeProcess }), ML = { systemPrompt: G, appendSystemPrompt: U, agents: N, promptSuggestions: W.promptSuggestions, agentProgressSummaries: W.agentProgressSummaries };
  return { queryInstance: new pX(hU, X, L, p, K, gU, yU, ML, o0), transport: hU, abortController: K };
}
function KL($, X, J, Q) {
  if (typeof J === "string") X.write(q$({ type: "user", session_id: "", message: { role: "user", content: [{ type: "text", text: J }] }, parent_tool_use_id: null }) + `
`);
  else $.streamInput(J).catch((Y) => Q.abort(Y));
}
function Qs({ prompt: $, options: X }) {
  let { queryInstance: J, transport: Q, abortController: Y } = HL(X, typeof $ === "string");
  return KL(J, Q, $, Y), J;
}

// src/control-file.ts
var import_node_fs = require("node:fs");
var import_promises4 = require("node:fs/promises");
function parseControlLine(line) {
  const trimmed = line.trim();
  if (!trimmed) return null;
  let obj;
  try {
    obj = JSON.parse(trimmed);
  } catch {
    return null;
  }
  if (!obj || typeof obj !== "object") return null;
  const rec = obj;
  switch (rec.type) {
    case "message": {
      const text = typeof rec.text === "string" ? rec.text : "";
      if (!text) return null;
      return { type: "message", text };
    }
    case "interrupt":
      return { type: "interrupt" };
    case "shutdown":
      return { type: "shutdown" };
    default:
      return null;
  }
}
var ControlFileReader = class {
  path;
  cursor = 0;
  buffer = "";
  watcher = null;
  pending = [];
  waiters = [];
  closed = false;
  /**
   * Short interval to re-check the file in case `fs.watch` misses an event
   * (e.g. on some filesystems where append-only writes don't always fire
   * watchers). This keeps the reader responsive without hammering the FS.
   */
  pollIntervalMs = 200;
  pollTimer = null;
  constructor(path3) {
    this.path = path3;
  }
  /** Begin watching the control file. Safe to call even if the file does not exist yet. */
  start() {
    if (this.closed) return;
    this.drainNewContent().catch(() => void 0);
    try {
      this.watcher = (0, import_node_fs.watch)(this.path, { persistent: false }, () => {
        this.drainNewContent().catch(() => void 0);
      });
      this.watcher.on("error", () => {
      });
    } catch {
    }
    this.pollTimer = setInterval(() => {
      this.drainNewContent().catch(() => void 0);
    }, this.pollIntervalMs);
    this.pollTimer.unref?.();
  }
  /** Stop watching and resolve any pending waiters with null (EOF sentinel). */
  close() {
    if (this.closed) return;
    this.closed = true;
    if (this.watcher) {
      try {
        this.watcher.close();
      } catch {
      }
      this.watcher = null;
    }
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const waiter of this.waiters.splice(0)) {
      waiter(null);
    }
  }
  /**
   * Wait for the next control command. Resolves with null when `close()`
   * has been called and no commands remain in the queue.
   */
  next() {
    if (this.pending.length > 0) {
      return Promise.resolve(this.pending.shift() ?? null);
    }
    if (this.closed) {
      return Promise.resolve(null);
    }
    return new Promise((resolve) => {
      this.waiters.push(resolve);
    });
  }
  /** Expose an AsyncIterable so drivers can `for await (const cmd of reader)`. */
  [Symbol.asyncIterator]() {
    return {
      next: async () => {
        const cmd = await this.next();
        if (cmd === null) return { value: void 0, done: true };
        return { value: cmd, done: false };
      }
    };
  }
  /**
   * Read any newly-appended bytes from the control file, split into lines,
   * parse each line as a control command, and enqueue them.
   */
  async drainNewContent() {
    if (this.closed) return;
    let size;
    try {
      const st = await (0, import_promises4.stat)(this.path);
      size = st.size;
    } catch {
      return;
    }
    if (size <= this.cursor) return;
    let fh = null;
    try {
      fh = await (0, import_promises4.open)(this.path, "r");
      const length = size - this.cursor;
      const buf = Buffer.alloc(length);
      await fh.read(buf, 0, length, this.cursor);
      this.cursor = size;
      this.buffer += buf.toString("utf8");
    } catch {
      return;
    } finally {
      if (fh) await fh.close().catch(() => void 0);
    }
    let idx;
    while ((idx = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, idx);
      this.buffer = this.buffer.slice(idx + 1);
      const cmd = parseControlLine(line);
      if (cmd) this.enqueue(cmd);
    }
  }
  enqueue(cmd) {
    const waiter = this.waiters.shift();
    if (waiter) {
      waiter(cmd);
      return;
    }
    this.pending.push(cmd);
  }
};

// src/jsonl.ts
function emit(event) {
  try {
    process.stdout.write(JSON.stringify(event) + "\n");
  } catch (err) {
    process.stdout.write(
      JSON.stringify({
        type: "error",
        message: `spawn-driver: failed to serialize event: ${err instanceof Error ? err.message : String(err)}`
      }) + "\n"
    );
  }
}
function emitFatal(message) {
  emit({ type: "error", message: `spawn-driver: ${message}` });
}

// src/claude-driver.ts
async function runClaudeDriver(args) {
  if (args.dryRun) {
    emitDryRun(args);
    return 0;
  }
  if (!args.task) {
    emitFatal("claude-driver: --task is required");
    return 1;
  }
  const options = {
    cwd: args.workingDir || void 0,
    systemPrompt: { type: "preset", preset: "claude_code" },
    permissionMode: "bypassPermissions",
    includePartialMessages: false
  };
  if (args.maxTurns > 0) {
    options.maxTurns = args.maxTurns;
  }
  if (args.maxCostUsd > 0) {
    options.maxBudgetUsd = args.maxCostUsd;
  }
  if (args.multiTurn) {
    return runMultiTurn(args, options);
  }
  return runSingleShot(args, options);
}
async function runSingleShot(args, options) {
  let exitCode = 0;
  try {
    const stream = Qs({ prompt: args.task, options });
    for await (const message of stream) {
      forwardMessage(message);
      if (message.type === "result" && message.is_error) {
        exitCode = 1;
      }
    }
  } catch (err) {
    emitFatal(
      `claude-driver runtime error: ${err instanceof Error ? err.message : String(err)}`
    );
    return 1;
  }
  return exitCode;
}
async function runMultiTurn(args, options) {
  const inputQueue = [args.task];
  let inputResolve = null;
  let inputClosed = false;
  const pushInput = (text) => {
    if (inputClosed) return;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(text);
      return;
    }
    inputQueue.push(text);
  };
  const closeInput = () => {
    if (inputClosed) return;
    inputClosed = true;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(null);
    }
  };
  const nextInput = () => {
    if (inputQueue.length > 0) {
      return Promise.resolve(inputQueue.shift() ?? null);
    }
    if (inputClosed) return Promise.resolve(null);
    return new Promise((resolve) => {
      inputResolve = resolve;
    });
  };
  async function* userMessageStream() {
    while (true) {
      const text = await nextInput();
      if (text === null) return;
      yield {
        type: "user",
        message: { role: "user", content: text },
        parent_tool_use_id: null
      };
    }
  }
  const controlReader = args.controlFile ? new ControlFileReader(args.controlFile) : null;
  controlReader?.start();
  const queryRef = { current: null };
  let exitCode = 0;
  const controlPump = (async () => {
    if (!controlReader) return;
    for await (const cmd of controlReader) {
      switch (cmd.type) {
        case "message":
          pushInput(cmd.text);
          break;
        case "interrupt": {
          const activeQuery = queryRef.current;
          if (!activeQuery) break;
          try {
            await activeQuery.interrupt();
          } catch (err) {
            emit({
              type: "error",
              message: `claude-driver: interrupt failed: ${err instanceof Error ? err.message : String(err)}`
            });
          }
          break;
        }
        case "shutdown":
          closeInput();
          return;
      }
    }
  })();
  try {
    queryRef.current = Qs({ prompt: userMessageStream(), options });
    for await (const message of queryRef.current) {
      forwardMessage(message);
      if (message.type === "result") {
        if (message.is_error) exitCode = 1;
      }
    }
  } catch (err) {
    emitFatal(
      `claude-driver runtime error: ${err instanceof Error ? err.message : String(err)}`
    );
    exitCode = 1;
  } finally {
    closeInput();
    controlReader?.close();
    await controlPump.catch(() => void 0);
    try {
      queryRef.current?.close();
    } catch {
    }
  }
  return exitCode;
}
function forwardMessage(message) {
  switch (message.type) {
    case "assistant":
    case "user":
    case "result":
    case "system":
      emit(message);
      return;
    default:
      return;
  }
}
function emitDryRun(args) {
  const sessionId = `dryrun-claude-${args.spawnId || "unknown"}`;
  emit({ type: "system", subtype: "init", session_id: sessionId });
  emit({
    type: "assistant",
    session_id: sessionId,
    message: {
      id: "msg_dryrun_1",
      usage: {
        input_tokens: 4,
        output_tokens: 4,
        cache_creation_input_tokens: 0,
        cache_read_input_tokens: 0
      },
      content: [
        {
          type: "text",
          text: `[loom-spawn-driver dry-run] would invoke claude SDK for: ${args.task || "(no task)"}`
        }
      ]
    }
  });
  emit({
    type: "result",
    subtype: "success",
    session_id: sessionId,
    duration_ms: 1,
    num_turns: 0,
    total_cost_usd: 0,
    result: "dry-run completed without invoking the SDK."
  });
}

// node_modules/@openai/codex-sdk/dist/index.js
var import_fs2 = require("fs");
var import_os2 = __toESM(require("os"), 1);
var import_path5 = __toESM(require("path"), 1);
var import_child_process3 = require("child_process");
var import_path6 = __toESM(require("path"), 1);
var import_readline2 = __toESM(require("readline"), 1);
var import_module = require("module");
async function createOutputSchemaFile(schema) {
  if (schema === void 0) {
    return { cleanup: async () => {
    } };
  }
  if (!isJsonObject(schema)) {
    throw new Error("outputSchema must be a plain JSON object");
  }
  const schemaDir = await import_fs2.promises.mkdtemp(import_path5.default.join(import_os2.default.tmpdir(), "codex-output-schema-"));
  const schemaPath = import_path5.default.join(schemaDir, "schema.json");
  const cleanup = async () => {
    try {
      await import_fs2.promises.rm(schemaDir, { recursive: true, force: true });
    } catch {
    }
  };
  try {
    await import_fs2.promises.writeFile(schemaPath, JSON.stringify(schema), "utf8");
    return { schemaPath, cleanup };
  } catch (error) {
    await cleanup();
    throw error;
  }
}
function isJsonObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
var Thread = class {
  _exec;
  _options;
  _id;
  _threadOptions;
  /** Returns the ID of the thread. Populated after the first turn starts. */
  get id() {
    return this._id;
  }
  /* @internal */
  constructor(exec, options, threadOptions, id = null) {
    this._exec = exec;
    this._options = options;
    this._id = id;
    this._threadOptions = threadOptions;
  }
  /** Provides the input to the agent and streams events as they are produced during the turn. */
  async runStreamed(input, turnOptions = {}) {
    return { events: this.runStreamedInternal(input, turnOptions) };
  }
  async *runStreamedInternal(input, turnOptions = {}) {
    const { schemaPath, cleanup } = await createOutputSchemaFile(turnOptions.outputSchema);
    const options = this._threadOptions;
    const { prompt, images } = normalizeInput(input);
    const generator = this._exec.run({
      input: prompt,
      baseUrl: this._options.baseUrl,
      apiKey: this._options.apiKey,
      threadId: this._id,
      images,
      model: options?.model,
      sandboxMode: options?.sandboxMode,
      workingDirectory: options?.workingDirectory,
      skipGitRepoCheck: options?.skipGitRepoCheck,
      outputSchemaFile: schemaPath,
      modelReasoningEffort: options?.modelReasoningEffort,
      signal: turnOptions.signal,
      networkAccessEnabled: options?.networkAccessEnabled,
      webSearchMode: options?.webSearchMode,
      webSearchEnabled: options?.webSearchEnabled,
      approvalPolicy: options?.approvalPolicy,
      additionalDirectories: options?.additionalDirectories
    });
    try {
      for await (const item of generator) {
        let parsed;
        try {
          parsed = JSON.parse(item);
        } catch (error) {
          throw new Error(`Failed to parse item: ${item}`, { cause: error });
        }
        if (parsed.type === "thread.started") {
          this._id = parsed.thread_id;
        }
        yield parsed;
      }
    } finally {
      await cleanup();
    }
  }
  /** Provides the input to the agent and returns the completed turn. */
  async run(input, turnOptions = {}) {
    const generator = this.runStreamedInternal(input, turnOptions);
    const items = [];
    let finalResponse = "";
    let usage = null;
    let turnFailure = null;
    for await (const event of generator) {
      if (event.type === "item.completed") {
        if (event.item.type === "agent_message") {
          finalResponse = event.item.text;
        }
        items.push(event.item);
      } else if (event.type === "turn.completed") {
        usage = event.usage;
      } else if (event.type === "turn.failed") {
        turnFailure = event.error;
        break;
      }
    }
    if (turnFailure) {
      throw new Error(turnFailure.message);
    }
    return { items, finalResponse, usage };
  }
};
function normalizeInput(input) {
  if (typeof input === "string") {
    return { prompt: input, images: [] };
  }
  const promptParts = [];
  const images = [];
  for (const item of input) {
    if (item.type === "text") {
      promptParts.push(item.text);
    } else if (item.type === "local_image") {
      images.push(item.path);
    }
  }
  return { prompt: promptParts.join("\n\n"), images };
}
var INTERNAL_ORIGINATOR_ENV = "CODEX_INTERNAL_ORIGINATOR_OVERRIDE";
var TYPESCRIPT_SDK_ORIGINATOR = "codex_sdk_ts";
var CODEX_NPM_NAME = "@openai/codex";
var PLATFORM_PACKAGE_BY_TARGET = {
  "x86_64-unknown-linux-musl": "@openai/codex-linux-x64",
  "aarch64-unknown-linux-musl": "@openai/codex-linux-arm64",
  "x86_64-apple-darwin": "@openai/codex-darwin-x64",
  "aarch64-apple-darwin": "@openai/codex-darwin-arm64",
  "x86_64-pc-windows-msvc": "@openai/codex-win32-x64",
  "aarch64-pc-windows-msvc": "@openai/codex-win32-arm64"
};
var moduleRequire = (0, import_module.createRequire)(__loom_spawn_driver_meta_url);
var CodexExec = class {
  executablePath;
  envOverride;
  configOverrides;
  constructor(executablePath = null, env, configOverrides) {
    this.executablePath = executablePath || findCodexPath();
    this.envOverride = env;
    this.configOverrides = configOverrides;
  }
  async *run(args) {
    const commandArgs = ["exec", "--experimental-json"];
    if (this.configOverrides) {
      for (const override of serializeConfigOverrides(this.configOverrides)) {
        commandArgs.push("--config", override);
      }
    }
    if (args.baseUrl) {
      commandArgs.push(
        "--config",
        `openai_base_url=${toTomlValue(args.baseUrl, "openai_base_url")}`
      );
    }
    if (args.model) {
      commandArgs.push("--model", args.model);
    }
    if (args.sandboxMode) {
      commandArgs.push("--sandbox", args.sandboxMode);
    }
    if (args.workingDirectory) {
      commandArgs.push("--cd", args.workingDirectory);
    }
    if (args.additionalDirectories?.length) {
      for (const dir of args.additionalDirectories) {
        commandArgs.push("--add-dir", dir);
      }
    }
    if (args.skipGitRepoCheck) {
      commandArgs.push("--skip-git-repo-check");
    }
    if (args.outputSchemaFile) {
      commandArgs.push("--output-schema", args.outputSchemaFile);
    }
    if (args.modelReasoningEffort) {
      commandArgs.push("--config", `model_reasoning_effort="${args.modelReasoningEffort}"`);
    }
    if (args.networkAccessEnabled !== void 0) {
      commandArgs.push(
        "--config",
        `sandbox_workspace_write.network_access=${args.networkAccessEnabled}`
      );
    }
    if (args.webSearchMode) {
      commandArgs.push("--config", `web_search="${args.webSearchMode}"`);
    } else if (args.webSearchEnabled === true) {
      commandArgs.push("--config", `web_search="live"`);
    } else if (args.webSearchEnabled === false) {
      commandArgs.push("--config", `web_search="disabled"`);
    }
    if (args.approvalPolicy) {
      commandArgs.push("--config", `approval_policy="${args.approvalPolicy}"`);
    }
    if (args.threadId) {
      commandArgs.push("resume", args.threadId);
    }
    if (args.images?.length) {
      for (const image of args.images) {
        commandArgs.push("--image", image);
      }
    }
    const env = {};
    if (this.envOverride) {
      Object.assign(env, this.envOverride);
    } else {
      for (const [key, value] of Object.entries(process.env)) {
        if (value !== void 0) {
          env[key] = value;
        }
      }
    }
    if (!env[INTERNAL_ORIGINATOR_ENV]) {
      env[INTERNAL_ORIGINATOR_ENV] = TYPESCRIPT_SDK_ORIGINATOR;
    }
    if (args.apiKey) {
      env.CODEX_API_KEY = args.apiKey;
    }
    const child = (0, import_child_process3.spawn)(this.executablePath, commandArgs, {
      env,
      signal: args.signal
    });
    let spawnError = null;
    child.once("error", (err) => spawnError = err);
    if (!child.stdin) {
      child.kill();
      throw new Error("Child process has no stdin");
    }
    child.stdin.write(args.input);
    child.stdin.end();
    if (!child.stdout) {
      child.kill();
      throw new Error("Child process has no stdout");
    }
    const stderrChunks = [];
    if (child.stderr) {
      child.stderr.on("data", (data) => {
        stderrChunks.push(data);
      });
    }
    const exitPromise = new Promise(
      (resolve) => {
        child.once("exit", (code, signal) => {
          resolve({ code, signal });
        });
      }
    );
    const rl2 = import_readline2.default.createInterface({
      input: child.stdout,
      crlfDelay: Infinity
    });
    try {
      for await (const line of rl2) {
        yield line;
      }
      if (spawnError) throw spawnError;
      const { code, signal } = await exitPromise;
      if (code !== 0 || signal) {
        const stderrBuffer = Buffer.concat(stderrChunks);
        const detail = signal ? `signal ${signal}` : `code ${code ?? 1}`;
        throw new Error(`Codex Exec exited with ${detail}: ${stderrBuffer.toString("utf8")}`);
      }
    } finally {
      rl2.close();
      child.removeAllListeners();
      try {
        if (!child.killed) child.kill();
      } catch {
      }
    }
  }
};
function serializeConfigOverrides(configOverrides) {
  const overrides = [];
  flattenConfigOverrides(configOverrides, "", overrides);
  return overrides;
}
function flattenConfigOverrides(value, prefix, overrides) {
  if (!isPlainObject(value)) {
    if (prefix) {
      overrides.push(`${prefix}=${toTomlValue(value, prefix)}`);
      return;
    } else {
      throw new Error("Codex config overrides must be a plain object");
    }
  }
  const entries = Object.entries(value);
  if (!prefix && entries.length === 0) {
    return;
  }
  if (prefix && entries.length === 0) {
    overrides.push(`${prefix}={}`);
    return;
  }
  for (const [key, child] of entries) {
    if (!key) {
      throw new Error("Codex config override keys must be non-empty strings");
    }
    if (child === void 0) {
      continue;
    }
    const path3 = prefix ? `${prefix}.${key}` : key;
    if (isPlainObject(child)) {
      flattenConfigOverrides(child, path3, overrides);
    } else {
      overrides.push(`${path3}=${toTomlValue(child, path3)}`);
    }
  }
}
function toTomlValue(value, path3) {
  if (typeof value === "string") {
    return JSON.stringify(value);
  } else if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new Error(`Codex config override at ${path3} must be a finite number`);
    }
    return `${value}`;
  } else if (typeof value === "boolean") {
    return value ? "true" : "false";
  } else if (Array.isArray(value)) {
    const rendered = value.map((item, index) => toTomlValue(item, `${path3}[${index}]`));
    return `[${rendered.join(", ")}]`;
  } else if (isPlainObject(value)) {
    const parts = [];
    for (const [key, child] of Object.entries(value)) {
      if (!key) {
        throw new Error("Codex config override keys must be non-empty strings");
      }
      if (child === void 0) {
        continue;
      }
      parts.push(`${formatTomlKey(key)} = ${toTomlValue(child, `${path3}.${key}`)}`);
    }
    return `{${parts.join(", ")}}`;
  } else if (value === null) {
    throw new Error(`Codex config override at ${path3} cannot be null`);
  } else {
    const typeName = typeof value;
    throw new Error(`Unsupported Codex config override value at ${path3}: ${typeName}`);
  }
}
var TOML_BARE_KEY = /^[A-Za-z0-9_-]+$/;
function formatTomlKey(key) {
  return TOML_BARE_KEY.test(key) ? key : JSON.stringify(key);
}
function isPlainObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
function findCodexPath() {
  const { platform, arch } = process;
  let targetTriple = null;
  switch (platform) {
    case "linux":
    case "android":
      switch (arch) {
        case "x64":
          targetTriple = "x86_64-unknown-linux-musl";
          break;
        case "arm64":
          targetTriple = "aarch64-unknown-linux-musl";
          break;
        default:
          break;
      }
      break;
    case "darwin":
      switch (arch) {
        case "x64":
          targetTriple = "x86_64-apple-darwin";
          break;
        case "arm64":
          targetTriple = "aarch64-apple-darwin";
          break;
        default:
          break;
      }
      break;
    case "win32":
      switch (arch) {
        case "x64":
          targetTriple = "x86_64-pc-windows-msvc";
          break;
        case "arm64":
          targetTriple = "aarch64-pc-windows-msvc";
          break;
        default:
          break;
      }
      break;
    default:
      break;
  }
  if (!targetTriple) {
    throw new Error(`Unsupported platform: ${platform} (${arch})`);
  }
  const platformPackage = PLATFORM_PACKAGE_BY_TARGET[targetTriple];
  if (!platformPackage) {
    throw new Error(`Unsupported target triple: ${targetTriple}`);
  }
  let vendorRoot;
  try {
    const codexPackageJsonPath = moduleRequire.resolve(`${CODEX_NPM_NAME}/package.json`);
    const codexRequire = (0, import_module.createRequire)(codexPackageJsonPath);
    const platformPackageJsonPath = codexRequire.resolve(`${platformPackage}/package.json`);
    vendorRoot = import_path6.default.join(import_path6.default.dirname(platformPackageJsonPath), "vendor");
  } catch {
    throw new Error(
      `Unable to locate Codex CLI binaries. Ensure ${CODEX_NPM_NAME} is installed with optional dependencies.`
    );
  }
  const archRoot = import_path6.default.join(vendorRoot, targetTriple);
  const codexBinaryName = process.platform === "win32" ? "codex.exe" : "codex";
  const binaryPath = import_path6.default.join(archRoot, "codex", codexBinaryName);
  return binaryPath;
}
var Codex = class {
  exec;
  options;
  constructor(options = {}) {
    const { codexPathOverride, env, config } = options;
    this.exec = new CodexExec(codexPathOverride, env, config);
    this.options = options;
  }
  /**
   * Starts a new conversation with an agent.
   * @returns A new thread instance.
   */
  startThread(options = {}) {
    return new Thread(this.exec, this.options, options);
  }
  /**
   * Resumes a conversation with an agent based on the thread id.
   * Threads are persisted in ~/.codex/sessions.
   *
   * @param id The id of the thread to resume.
   * @returns A new thread instance.
   */
  resumeThread(id, options = {}) {
    return new Thread(this.exec, this.options, options, id);
  }
};

// src/codex-driver.ts
async function runCodexDriver(args) {
  if (args.dryRun) {
    emitDryRun2(args);
    return 0;
  }
  if (!args.task) {
    emitFatal("codex-driver: --task is required");
    return 1;
  }
  const threadOptions = {
    sandboxMode: "workspace-write",
    networkAccessEnabled: true,
    approvalPolicy: "never",
    skipGitRepoCheck: true
  };
  if (args.workingDir) {
    threadOptions.workingDirectory = args.workingDir;
  }
  if (args.multiTurn) {
    return runMultiTurn2(args, threadOptions);
  }
  return runSingleShot2(args, threadOptions);
}
async function runSingleShot2(args, threadOptions) {
  let exitCode = 0;
  try {
    const codex = new Codex();
    const thread = codex.startThread(threadOptions);
    const { events } = await thread.runStreamed(args.task);
    for await (const event of events) {
      forwardEvent(event);
      if (event.type === "turn.failed" || event.type === "error") {
        exitCode = 1;
      }
    }
  } catch (err) {
    emitFatal(
      `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`
    );
    return 1;
  }
  return exitCode;
}
async function runMultiTurn2(args, threadOptions) {
  const inputQueue = [args.task];
  let inputResolve = null;
  let inputClosed = false;
  const pushInput = (text) => {
    if (inputClosed) return;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(text);
      return;
    }
    inputQueue.push(text);
  };
  const closeInput = () => {
    if (inputClosed) return;
    inputClosed = true;
    if (inputResolve) {
      const resolve = inputResolve;
      inputResolve = null;
      resolve(null);
    }
  };
  const nextInput = () => {
    if (inputQueue.length > 0) {
      return Promise.resolve(inputQueue.shift() ?? null);
    }
    if (inputClosed) return Promise.resolve(null);
    return new Promise((resolve) => {
      inputResolve = resolve;
    });
  };
  const controlReader = args.controlFile ? new ControlFileReader(args.controlFile) : null;
  controlReader?.start();
  const acRef = { current: null };
  let exitCode = 0;
  const controlPump = (async () => {
    if (!controlReader) return;
    for await (const cmd of controlReader) {
      switch (cmd.type) {
        case "message":
          pushInput(cmd.text);
          break;
        case "interrupt": {
          const ac = acRef.current;
          if (!ac) break;
          try {
            ac.abort();
          } catch {
          }
          break;
        }
        case "shutdown":
          closeInput();
          return;
      }
    }
  })();
  try {
    const codex = new Codex();
    const thread = codex.startThread(threadOptions);
    while (true) {
      const text = await nextInput();
      if (text === null) break;
      const ac = new AbortController();
      acRef.current = ac;
      try {
        const { events } = await thread.runStreamed(text, { signal: ac.signal });
        for await (const event of events) {
          forwardEvent(event);
          if (event.type === "turn.failed" || event.type === "error") {
            exitCode = 1;
          }
        }
      } catch (err) {
        if (ac.signal.aborted) {
          emit({
            type: "error",
            message: `codex-driver: turn aborted by interrupt`
          });
        } else {
          emitFatal(
            `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`
          );
          exitCode = 1;
          break;
        }
      } finally {
        acRef.current = null;
      }
    }
  } catch (err) {
    emitFatal(
      `codex-driver runtime error: ${err instanceof Error ? err.message : String(err)}`
    );
    exitCode = 1;
  } finally {
    closeInput();
    controlReader?.close();
    await controlPump.catch(() => void 0);
  }
  return exitCode;
}
function forwardEvent(event) {
  switch (event.type) {
    case "thread.started":
    case "turn.started":
    case "turn.completed":
    case "turn.failed":
    case "error":
      emit(event);
      return;
    case "item.started":
    case "item.completed":
      emit({ type: event.type, item: transformItem(event.item) });
      return;
    case "item.updated":
      return;
    default: {
      const exhaustive = event;
      emit(event);
      return;
    }
  }
}
function transformItem(item) {
  const base = { ...item };
  switch (item.type) {
    case "command_execution": {
      if (item.aggregated_output && !("stderr" in base)) {
        base.stderr = item.aggregated_output;
      }
      return base;
    }
    case "mcp_tool_call": {
      if (item.error?.message) {
        base.error = item.error.message;
      } else {
        delete base.error;
      }
      return base;
    }
    case "todo_list": {
      const lines = item.items.map((todo) => `${todo.completed ? "[x]" : "[ ]"} ${todo.text}`).join("\n");
      base.text = lines;
      return base;
    }
    case "agent_message":
    case "reasoning":
    case "error":
    case "file_change":
    case "web_search":
      return base;
    default: {
      const exhaustive = item;
      return base;
    }
  }
}
function emitDryRun2(args) {
  const threadId = `dryrun-codex-${args.spawnId || "unknown"}`;
  emit({ type: "thread.started", thread_id: threadId });
  emit({ type: "turn.started" });
  emit({
    type: "item.completed",
    item: {
      id: "item_dryrun_1",
      type: "agent_message",
      text: `[loom-spawn-driver dry-run] would invoke codex SDK for: ${args.task || "(no task)"}`
    }
  });
  emit({
    type: "turn.completed",
    usage: { input_tokens: 4, cached_input_tokens: 0, output_tokens: 4 }
  });
}

// src/index.ts
async function main() {
  const args = parseArgs(process.argv);
  let exitCode = 0;
  switch (args.agentType) {
    case "claude-code":
    case "claude":
      exitCode = await runClaudeDriver(args);
      break;
    case "codex":
      exitCode = await runCodexDriver(args);
      break;
    default:
      emitFatal(`unsupported agent type: ${args.agentType}`);
      exitCode = 1;
  }
  process.exit(exitCode);
}
main().catch((err) => {
  emitFatal(`unhandled top-level error: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
});
