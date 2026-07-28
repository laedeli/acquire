var Io = { exports: {} }, Cr = {}, Do = { exports: {} }, ne = {};
/**
 * @license React
 * react.production.min.js
 *
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
var $a;
function sf() {
  if ($a) return ne;
  $a = 1;
  var c = Symbol.for("react.element"), f = Symbol.for("react.portal"), s = Symbol.for("react.fragment"), y = Symbol.for("react.strict_mode"), m = Symbol.for("react.profiler"), x = Symbol.for("react.provider"), _ = Symbol.for("react.context"), k = Symbol.for("react.forward_ref"), M = Symbol.for("react.suspense"), I = Symbol.for("react.memo"), W = Symbol.for("react.lazy"), P = Symbol.iterator;
  function S(h) {
    return h === null || typeof h != "object" ? null : (h = P && h[P] || h["@@iterator"], typeof h == "function" ? h : null);
  }
  var O = { isMounted: function() {
    return !1;
  }, enqueueForceUpdate: function() {
  }, enqueueReplaceState: function() {
  }, enqueueSetState: function() {
  } }, X = Object.assign, N = {};
  function $(h, C, te) {
    this.props = h, this.context = C, this.refs = N, this.updater = te || O;
  }
  $.prototype.isReactComponent = {}, $.prototype.setState = function(h, C) {
    if (typeof h != "object" && typeof h != "function" && h != null) throw Error("setState(...): takes an object of state variables to update or a function which returns an object of state variables.");
    this.updater.enqueueSetState(this, h, C, "setState");
  }, $.prototype.forceUpdate = function(h) {
    this.updater.enqueueForceUpdate(this, h, "forceUpdate");
  };
  function E() {
  }
  E.prototype = $.prototype;
  function b(h, C, te) {
    this.props = h, this.context = C, this.refs = N, this.updater = te || O;
  }
  var G = b.prototype = new E();
  G.constructor = b, X(G, $.prototype), G.isPureReactComponent = !0;
  var ee = Array.isArray, fe = Object.prototype.hasOwnProperty, ge = { current: null }, q = { key: !0, ref: !0, __self: !0, __source: !0 };
  function D(h, C, te) {
    var re, oe = {}, ue = null, he = null;
    if (C != null) for (re in C.ref !== void 0 && (he = C.ref), C.key !== void 0 && (ue = "" + C.key), C) fe.call(C, re) && !q.hasOwnProperty(re) && (oe[re] = C[re]);
    var ce = arguments.length - 2;
    if (ce === 1) oe.children = te;
    else if (1 < ce) {
      for (var we = Array(ce), Ze = 0; Ze < ce; Ze++) we[Ze] = arguments[Ze + 2];
      oe.children = we;
    }
    if (h && h.defaultProps) for (re in ce = h.defaultProps, ce) oe[re] === void 0 && (oe[re] = ce[re]);
    return { $$typeof: c, type: h, key: ue, ref: he, props: oe, _owner: ge.current };
  }
  function pe(h, C) {
    return { $$typeof: c, type: h.type, key: C, ref: h.ref, props: h.props, _owner: h._owner };
  }
  function ae(h) {
    return typeof h == "object" && h !== null && h.$$typeof === c;
  }
  function Oe(h) {
    var C = { "=": "=0", ":": "=2" };
    return "$" + h.replace(/[=:]/g, function(te) {
      return C[te];
    });
  }
  var rt = /\/+/g;
  function Fe(h, C) {
    return typeof h == "object" && h !== null && h.key != null ? Oe("" + h.key) : C.toString(36);
  }
  function ct(h, C, te, re, oe) {
    var ue = typeof h;
    (ue === "undefined" || ue === "boolean") && (h = null);
    var he = !1;
    if (h === null) he = !0;
    else switch (ue) {
      case "string":
      case "number":
        he = !0;
        break;
      case "object":
        switch (h.$$typeof) {
          case c:
          case f:
            he = !0;
        }
    }
    if (he) return he = h, oe = oe(he), h = re === "" ? "." + Fe(he, 0) : re, ee(oe) ? (te = "", h != null && (te = h.replace(rt, "$&/") + "/"), ct(oe, C, te, "", function(Ze) {
      return Ze;
    })) : oe != null && (ae(oe) && (oe = pe(oe, te + (!oe.key || he && he.key === oe.key ? "" : ("" + oe.key).replace(rt, "$&/") + "/") + h)), C.push(oe)), 1;
    if (he = 0, re = re === "" ? "." : re + ":", ee(h)) for (var ce = 0; ce < h.length; ce++) {
      ue = h[ce];
      var we = re + Fe(ue, ce);
      he += ct(ue, C, te, we, oe);
    }
    else if (we = S(h), typeof we == "function") for (h = we.call(h), ce = 0; !(ue = h.next()).done; ) ue = ue.value, we = re + Fe(ue, ce++), he += ct(ue, C, te, we, oe);
    else if (ue === "object") throw C = String(h), Error("Objects are not valid as a React child (found: " + (C === "[object Object]" ? "object with keys {" + Object.keys(h).join(", ") + "}" : C) + "). If you meant to render a collection of children, use an array instead.");
    return he;
  }
  function gt(h, C, te) {
    if (h == null) return h;
    var re = [], oe = 0;
    return ct(h, re, "", "", function(ue) {
      return C.call(te, ue, oe++);
    }), re;
  }
  function Qe(h) {
    if (h._status === -1) {
      var C = h._result;
      C = C(), C.then(function(te) {
        (h._status === 0 || h._status === -1) && (h._status = 1, h._result = te);
      }, function(te) {
        (h._status === 0 || h._status === -1) && (h._status = 2, h._result = te);
      }), h._status === -1 && (h._status = 0, h._result = C);
    }
    if (h._status === 1) return h._result.default;
    throw h._result;
  }
  var _e = { current: null }, F = { transition: null }, J = { ReactCurrentDispatcher: _e, ReactCurrentBatchConfig: F, ReactCurrentOwner: ge };
  function B() {
    throw Error("act(...) is not supported in production builds of React.");
  }
  return ne.Children = { map: gt, forEach: function(h, C, te) {
    gt(h, function() {
      C.apply(this, arguments);
    }, te);
  }, count: function(h) {
    var C = 0;
    return gt(h, function() {
      C++;
    }), C;
  }, toArray: function(h) {
    return gt(h, function(C) {
      return C;
    }) || [];
  }, only: function(h) {
    if (!ae(h)) throw Error("React.Children.only expected to receive a single React element child.");
    return h;
  } }, ne.Component = $, ne.Fragment = s, ne.Profiler = m, ne.PureComponent = b, ne.StrictMode = y, ne.Suspense = M, ne.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED = J, ne.act = B, ne.cloneElement = function(h, C, te) {
    if (h == null) throw Error("React.cloneElement(...): The argument must be a React element, but you passed " + h + ".");
    var re = X({}, h.props), oe = h.key, ue = h.ref, he = h._owner;
    if (C != null) {
      if (C.ref !== void 0 && (ue = C.ref, he = ge.current), C.key !== void 0 && (oe = "" + C.key), h.type && h.type.defaultProps) var ce = h.type.defaultProps;
      for (we in C) fe.call(C, we) && !q.hasOwnProperty(we) && (re[we] = C[we] === void 0 && ce !== void 0 ? ce[we] : C[we]);
    }
    var we = arguments.length - 2;
    if (we === 1) re.children = te;
    else if (1 < we) {
      ce = Array(we);
      for (var Ze = 0; Ze < we; Ze++) ce[Ze] = arguments[Ze + 2];
      re.children = ce;
    }
    return { $$typeof: c, type: h.type, key: oe, ref: ue, props: re, _owner: he };
  }, ne.createContext = function(h) {
    return h = { $$typeof: _, _currentValue: h, _currentValue2: h, _threadCount: 0, Provider: null, Consumer: null, _defaultValue: null, _globalName: null }, h.Provider = { $$typeof: x, _context: h }, h.Consumer = h;
  }, ne.createElement = D, ne.createFactory = function(h) {
    var C = D.bind(null, h);
    return C.type = h, C;
  }, ne.createRef = function() {
    return { current: null };
  }, ne.forwardRef = function(h) {
    return { $$typeof: k, render: h };
  }, ne.isValidElement = ae, ne.lazy = function(h) {
    return { $$typeof: W, _payload: { _status: -1, _result: h }, _init: Qe };
  }, ne.memo = function(h, C) {
    return { $$typeof: I, type: h, compare: C === void 0 ? null : C };
  }, ne.startTransition = function(h) {
    var C = F.transition;
    F.transition = {};
    try {
      h();
    } finally {
      F.transition = C;
    }
  }, ne.unstable_act = B, ne.useCallback = function(h, C) {
    return _e.current.useCallback(h, C);
  }, ne.useContext = function(h) {
    return _e.current.useContext(h);
  }, ne.useDebugValue = function() {
  }, ne.useDeferredValue = function(h) {
    return _e.current.useDeferredValue(h);
  }, ne.useEffect = function(h, C) {
    return _e.current.useEffect(h, C);
  }, ne.useId = function() {
    return _e.current.useId();
  }, ne.useImperativeHandle = function(h, C, te) {
    return _e.current.useImperativeHandle(h, C, te);
  }, ne.useInsertionEffect = function(h, C) {
    return _e.current.useInsertionEffect(h, C);
  }, ne.useLayoutEffect = function(h, C) {
    return _e.current.useLayoutEffect(h, C);
  }, ne.useMemo = function(h, C) {
    return _e.current.useMemo(h, C);
  }, ne.useReducer = function(h, C, te) {
    return _e.current.useReducer(h, C, te);
  }, ne.useRef = function(h) {
    return _e.current.useRef(h);
  }, ne.useState = function(h) {
    return _e.current.useState(h);
  }, ne.useSyncExternalStore = function(h, C, te) {
    return _e.current.useSyncExternalStore(h, C, te);
  }, ne.useTransition = function() {
    return _e.current.useTransition();
  }, ne.version = "18.3.1", ne;
}
var Ua;
function Bo() {
  return Ua || (Ua = 1, Do.exports = sf()), Do.exports;
}
/**
 * @license React
 * react-jsx-runtime.production.min.js
 *
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
var Aa;
function af() {
  if (Aa) return Cr;
  Aa = 1;
  var c = Bo(), f = Symbol.for("react.element"), s = Symbol.for("react.fragment"), y = Object.prototype.hasOwnProperty, m = c.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED.ReactCurrentOwner, x = { key: !0, ref: !0, __self: !0, __source: !0 };
  function _(k, M, I) {
    var W, P = {}, S = null, O = null;
    I !== void 0 && (S = "" + I), M.key !== void 0 && (S = "" + M.key), M.ref !== void 0 && (O = M.ref);
    for (W in M) y.call(M, W) && !x.hasOwnProperty(W) && (P[W] = M[W]);
    if (k && k.defaultProps) for (W in M = k.defaultProps, M) P[W] === void 0 && (P[W] = M[W]);
    return { $$typeof: f, type: k, key: S, ref: O, props: P, _owner: m.current };
  }
  return Cr.Fragment = s, Cr.jsx = _, Cr.jsxs = _, Cr;
}
var Ba;
function cf() {
  return Ba || (Ba = 1, Io.exports = af()), Io.exports;
}
var u = cf(), Ul = {}, Oo = { exports: {} }, Je = {}, Fo = { exports: {} }, $o = {};
/**
 * @license React
 * scheduler.production.min.js
 *
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
var Va;
function df() {
  return Va || (Va = 1, (function(c) {
    function f(F, J) {
      var B = F.length;
      F.push(J);
      e: for (; 0 < B; ) {
        var h = B - 1 >>> 1, C = F[h];
        if (0 < m(C, J)) F[h] = J, F[B] = C, B = h;
        else break e;
      }
    }
    function s(F) {
      return F.length === 0 ? null : F[0];
    }
    function y(F) {
      if (F.length === 0) return null;
      var J = F[0], B = F.pop();
      if (B !== J) {
        F[0] = B;
        e: for (var h = 0, C = F.length, te = C >>> 1; h < te; ) {
          var re = 2 * (h + 1) - 1, oe = F[re], ue = re + 1, he = F[ue];
          if (0 > m(oe, B)) ue < C && 0 > m(he, oe) ? (F[h] = he, F[ue] = B, h = ue) : (F[h] = oe, F[re] = B, h = re);
          else if (ue < C && 0 > m(he, B)) F[h] = he, F[ue] = B, h = ue;
          else break e;
        }
      }
      return J;
    }
    function m(F, J) {
      var B = F.sortIndex - J.sortIndex;
      return B !== 0 ? B : F.id - J.id;
    }
    if (typeof performance == "object" && typeof performance.now == "function") {
      var x = performance;
      c.unstable_now = function() {
        return x.now();
      };
    } else {
      var _ = Date, k = _.now();
      c.unstable_now = function() {
        return _.now() - k;
      };
    }
    var M = [], I = [], W = 1, P = null, S = 3, O = !1, X = !1, N = !1, $ = typeof setTimeout == "function" ? setTimeout : null, E = typeof clearTimeout == "function" ? clearTimeout : null, b = typeof setImmediate < "u" ? setImmediate : null;
    typeof navigator < "u" && navigator.scheduling !== void 0 && navigator.scheduling.isInputPending !== void 0 && navigator.scheduling.isInputPending.bind(navigator.scheduling);
    function G(F) {
      for (var J = s(I); J !== null; ) {
        if (J.callback === null) y(I);
        else if (J.startTime <= F) y(I), J.sortIndex = J.expirationTime, f(M, J);
        else break;
        J = s(I);
      }
    }
    function ee(F) {
      if (N = !1, G(F), !X) if (s(M) !== null) X = !0, Qe(fe);
      else {
        var J = s(I);
        J !== null && _e(ee, J.startTime - F);
      }
    }
    function fe(F, J) {
      X = !1, N && (N = !1, E(D), D = -1), O = !0;
      var B = S;
      try {
        for (G(J), P = s(M); P !== null && (!(P.expirationTime > J) || F && !Oe()); ) {
          var h = P.callback;
          if (typeof h == "function") {
            P.callback = null, S = P.priorityLevel;
            var C = h(P.expirationTime <= J);
            J = c.unstable_now(), typeof C == "function" ? P.callback = C : P === s(M) && y(M), G(J);
          } else y(M);
          P = s(M);
        }
        if (P !== null) var te = !0;
        else {
          var re = s(I);
          re !== null && _e(ee, re.startTime - J), te = !1;
        }
        return te;
      } finally {
        P = null, S = B, O = !1;
      }
    }
    var ge = !1, q = null, D = -1, pe = 5, ae = -1;
    function Oe() {
      return !(c.unstable_now() - ae < pe);
    }
    function rt() {
      if (q !== null) {
        var F = c.unstable_now();
        ae = F;
        var J = !0;
        try {
          J = q(!0, F);
        } finally {
          J ? Fe() : (ge = !1, q = null);
        }
      } else ge = !1;
    }
    var Fe;
    if (typeof b == "function") Fe = function() {
      b(rt);
    };
    else if (typeof MessageChannel < "u") {
      var ct = new MessageChannel(), gt = ct.port2;
      ct.port1.onmessage = rt, Fe = function() {
        gt.postMessage(null);
      };
    } else Fe = function() {
      $(rt, 0);
    };
    function Qe(F) {
      q = F, ge || (ge = !0, Fe());
    }
    function _e(F, J) {
      D = $(function() {
        F(c.unstable_now());
      }, J);
    }
    c.unstable_IdlePriority = 5, c.unstable_ImmediatePriority = 1, c.unstable_LowPriority = 4, c.unstable_NormalPriority = 3, c.unstable_Profiling = null, c.unstable_UserBlockingPriority = 2, c.unstable_cancelCallback = function(F) {
      F.callback = null;
    }, c.unstable_continueExecution = function() {
      X || O || (X = !0, Qe(fe));
    }, c.unstable_forceFrameRate = function(F) {
      0 > F || 125 < F ? console.error("forceFrameRate takes a positive int between 0 and 125, forcing frame rates higher than 125 fps is not supported") : pe = 0 < F ? Math.floor(1e3 / F) : 5;
    }, c.unstable_getCurrentPriorityLevel = function() {
      return S;
    }, c.unstable_getFirstCallbackNode = function() {
      return s(M);
    }, c.unstable_next = function(F) {
      switch (S) {
        case 1:
        case 2:
        case 3:
          var J = 3;
          break;
        default:
          J = S;
      }
      var B = S;
      S = J;
      try {
        return F();
      } finally {
        S = B;
      }
    }, c.unstable_pauseExecution = function() {
    }, c.unstable_requestPaint = function() {
    }, c.unstable_runWithPriority = function(F, J) {
      switch (F) {
        case 1:
        case 2:
        case 3:
        case 4:
        case 5:
          break;
        default:
          F = 3;
      }
      var B = S;
      S = F;
      try {
        return J();
      } finally {
        S = B;
      }
    }, c.unstable_scheduleCallback = function(F, J, B) {
      var h = c.unstable_now();
      switch (typeof B == "object" && B !== null ? (B = B.delay, B = typeof B == "number" && 0 < B ? h + B : h) : B = h, F) {
        case 1:
          var C = -1;
          break;
        case 2:
          C = 250;
          break;
        case 5:
          C = 1073741823;
          break;
        case 4:
          C = 1e4;
          break;
        default:
          C = 5e3;
      }
      return C = B + C, F = { id: W++, callback: J, priorityLevel: F, startTime: B, expirationTime: C, sortIndex: -1 }, B > h ? (F.sortIndex = B, f(I, F), s(M) === null && F === s(I) && (N ? (E(D), D = -1) : N = !0, _e(ee, B - h))) : (F.sortIndex = C, f(M, F), X || O || (X = !0, Qe(fe))), F;
    }, c.unstable_shouldYield = Oe, c.unstable_wrapCallback = function(F) {
      var J = S;
      return function() {
        var B = S;
        S = J;
        try {
          return F.apply(this, arguments);
        } finally {
          S = B;
        }
      };
    };
  })($o)), $o;
}
var Wa;
function ff() {
  return Wa || (Wa = 1, Fo.exports = df()), Fo.exports;
}
/**
 * @license React
 * react-dom.production.min.js
 *
 * Copyright (c) Facebook, Inc. and its affiliates.
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */
var Ha;
function pf() {
  if (Ha) return Je;
  Ha = 1;
  var c = Bo(), f = ff();
  function s(e) {
    for (var t = "https://reactjs.org/docs/error-decoder.html?invariant=" + e, n = 1; n < arguments.length; n++) t += "&args[]=" + encodeURIComponent(arguments[n]);
    return "Minified React error #" + e + "; visit " + t + " for the full message or use the non-minified dev environment for full errors and additional helpful warnings.";
  }
  var y = /* @__PURE__ */ new Set(), m = {};
  function x(e, t) {
    _(e, t), _(e + "Capture", t);
  }
  function _(e, t) {
    for (m[e] = t, e = 0; e < t.length; e++) y.add(t[e]);
  }
  var k = !(typeof window > "u" || typeof window.document > "u" || typeof window.document.createElement > "u"), M = Object.prototype.hasOwnProperty, I = /^[:A-Z_a-z\u00C0-\u00D6\u00D8-\u00F6\u00F8-\u02FF\u0370-\u037D\u037F-\u1FFF\u200C-\u200D\u2070-\u218F\u2C00-\u2FEF\u3001-\uD7FF\uF900-\uFDCF\uFDF0-\uFFFD][:A-Z_a-z\u00C0-\u00D6\u00D8-\u00F6\u00F8-\u02FF\u0370-\u037D\u037F-\u1FFF\u200C-\u200D\u2070-\u218F\u2C00-\u2FEF\u3001-\uD7FF\uF900-\uFDCF\uFDF0-\uFFFD\-.0-9\u00B7\u0300-\u036F\u203F-\u2040]*$/, W = {}, P = {};
  function S(e) {
    return M.call(P, e) ? !0 : M.call(W, e) ? !1 : I.test(e) ? P[e] = !0 : (W[e] = !0, !1);
  }
  function O(e, t, n, r) {
    if (n !== null && n.type === 0) return !1;
    switch (typeof t) {
      case "function":
      case "symbol":
        return !0;
      case "boolean":
        return r ? !1 : n !== null ? !n.acceptsBooleans : (e = e.toLowerCase().slice(0, 5), e !== "data-" && e !== "aria-");
      default:
        return !1;
    }
  }
  function X(e, t, n, r) {
    if (t === null || typeof t > "u" || O(e, t, n, r)) return !0;
    if (r) return !1;
    if (n !== null) switch (n.type) {
      case 3:
        return !t;
      case 4:
        return t === !1;
      case 5:
        return isNaN(t);
      case 6:
        return isNaN(t) || 1 > t;
    }
    return !1;
  }
  function N(e, t, n, r, l, i, o) {
    this.acceptsBooleans = t === 2 || t === 3 || t === 4, this.attributeName = r, this.attributeNamespace = l, this.mustUseProperty = n, this.propertyName = e, this.type = t, this.sanitizeURL = i, this.removeEmptyString = o;
  }
  var $ = {};
  "children dangerouslySetInnerHTML defaultValue defaultChecked innerHTML suppressContentEditableWarning suppressHydrationWarning style".split(" ").forEach(function(e) {
    $[e] = new N(e, 0, !1, e, null, !1, !1);
  }), [["acceptCharset", "accept-charset"], ["className", "class"], ["htmlFor", "for"], ["httpEquiv", "http-equiv"]].forEach(function(e) {
    var t = e[0];
    $[t] = new N(t, 1, !1, e[1], null, !1, !1);
  }), ["contentEditable", "draggable", "spellCheck", "value"].forEach(function(e) {
    $[e] = new N(e, 2, !1, e.toLowerCase(), null, !1, !1);
  }), ["autoReverse", "externalResourcesRequired", "focusable", "preserveAlpha"].forEach(function(e) {
    $[e] = new N(e, 2, !1, e, null, !1, !1);
  }), "allowFullScreen async autoFocus autoPlay controls default defer disabled disablePictureInPicture disableRemotePlayback formNoValidate hidden loop noModule noValidate open playsInline readOnly required reversed scoped seamless itemScope".split(" ").forEach(function(e) {
    $[e] = new N(e, 3, !1, e.toLowerCase(), null, !1, !1);
  }), ["checked", "multiple", "muted", "selected"].forEach(function(e) {
    $[e] = new N(e, 3, !0, e, null, !1, !1);
  }), ["capture", "download"].forEach(function(e) {
    $[e] = new N(e, 4, !1, e, null, !1, !1);
  }), ["cols", "rows", "size", "span"].forEach(function(e) {
    $[e] = new N(e, 6, !1, e, null, !1, !1);
  }), ["rowSpan", "start"].forEach(function(e) {
    $[e] = new N(e, 5, !1, e.toLowerCase(), null, !1, !1);
  });
  var E = /[\-:]([a-z])/g;
  function b(e) {
    return e[1].toUpperCase();
  }
  "accent-height alignment-baseline arabic-form baseline-shift cap-height clip-path clip-rule color-interpolation color-interpolation-filters color-profile color-rendering dominant-baseline enable-background fill-opacity fill-rule flood-color flood-opacity font-family font-size font-size-adjust font-stretch font-style font-variant font-weight glyph-name glyph-orientation-horizontal glyph-orientation-vertical horiz-adv-x horiz-origin-x image-rendering letter-spacing lighting-color marker-end marker-mid marker-start overline-position overline-thickness paint-order panose-1 pointer-events rendering-intent shape-rendering stop-color stop-opacity strikethrough-position strikethrough-thickness stroke-dasharray stroke-dashoffset stroke-linecap stroke-linejoin stroke-miterlimit stroke-opacity stroke-width text-anchor text-decoration text-rendering underline-position underline-thickness unicode-bidi unicode-range units-per-em v-alphabetic v-hanging v-ideographic v-mathematical vector-effect vert-adv-y vert-origin-x vert-origin-y word-spacing writing-mode xmlns:xlink x-height".split(" ").forEach(function(e) {
    var t = e.replace(
      E,
      b
    );
    $[t] = new N(t, 1, !1, e, null, !1, !1);
  }), "xlink:actuate xlink:arcrole xlink:role xlink:show xlink:title xlink:type".split(" ").forEach(function(e) {
    var t = e.replace(E, b);
    $[t] = new N(t, 1, !1, e, "http://www.w3.org/1999/xlink", !1, !1);
  }), ["xml:base", "xml:lang", "xml:space"].forEach(function(e) {
    var t = e.replace(E, b);
    $[t] = new N(t, 1, !1, e, "http://www.w3.org/XML/1998/namespace", !1, !1);
  }), ["tabIndex", "crossOrigin"].forEach(function(e) {
    $[e] = new N(e, 1, !1, e.toLowerCase(), null, !1, !1);
  }), $.xlinkHref = new N("xlinkHref", 1, !1, "xlink:href", "http://www.w3.org/1999/xlink", !0, !1), ["src", "href", "action", "formAction"].forEach(function(e) {
    $[e] = new N(e, 1, !1, e.toLowerCase(), null, !0, !0);
  });
  function G(e, t, n, r) {
    var l = $.hasOwnProperty(t) ? $[t] : null;
    (l !== null ? l.type !== 0 : r || !(2 < t.length) || t[0] !== "o" && t[0] !== "O" || t[1] !== "n" && t[1] !== "N") && (X(t, n, l, r) && (n = null), r || l === null ? S(t) && (n === null ? e.removeAttribute(t) : e.setAttribute(t, "" + n)) : l.mustUseProperty ? e[l.propertyName] = n === null ? l.type === 3 ? !1 : "" : n : (t = l.attributeName, r = l.attributeNamespace, n === null ? e.removeAttribute(t) : (l = l.type, n = l === 3 || l === 4 && n === !0 ? "" : "" + n, r ? e.setAttributeNS(r, t, n) : e.setAttribute(t, n))));
  }
  var ee = c.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED, fe = Symbol.for("react.element"), ge = Symbol.for("react.portal"), q = Symbol.for("react.fragment"), D = Symbol.for("react.strict_mode"), pe = Symbol.for("react.profiler"), ae = Symbol.for("react.provider"), Oe = Symbol.for("react.context"), rt = Symbol.for("react.forward_ref"), Fe = Symbol.for("react.suspense"), ct = Symbol.for("react.suspense_list"), gt = Symbol.for("react.memo"), Qe = Symbol.for("react.lazy"), _e = Symbol.for("react.offscreen"), F = Symbol.iterator;
  function J(e) {
    return e === null || typeof e != "object" ? null : (e = F && e[F] || e["@@iterator"], typeof e == "function" ? e : null);
  }
  var B = Object.assign, h;
  function C(e) {
    if (h === void 0) try {
      throw Error();
    } catch (n) {
      var t = n.stack.trim().match(/\n( *(at )?)/);
      h = t && t[1] || "";
    }
    return `
` + h + e;
  }
  var te = !1;
  function re(e, t) {
    if (!e || te) return "";
    te = !0;
    var n = Error.prepareStackTrace;
    Error.prepareStackTrace = void 0;
    try {
      if (t) if (t = function() {
        throw Error();
      }, Object.defineProperty(t.prototype, "props", { set: function() {
        throw Error();
      } }), typeof Reflect == "object" && Reflect.construct) {
        try {
          Reflect.construct(t, []);
        } catch (w) {
          var r = w;
        }
        Reflect.construct(e, [], t);
      } else {
        try {
          t.call();
        } catch (w) {
          r = w;
        }
        e.call(t.prototype);
      }
      else {
        try {
          throw Error();
        } catch (w) {
          r = w;
        }
        e();
      }
    } catch (w) {
      if (w && r && typeof w.stack == "string") {
        for (var l = w.stack.split(`
`), i = r.stack.split(`
`), o = l.length - 1, a = i.length - 1; 1 <= o && 0 <= a && l[o] !== i[a]; ) a--;
        for (; 1 <= o && 0 <= a; o--, a--) if (l[o] !== i[a]) {
          if (o !== 1 || a !== 1)
            do
              if (o--, a--, 0 > a || l[o] !== i[a]) {
                var d = `
` + l[o].replace(" at new ", " at ");
                return e.displayName && d.includes("<anonymous>") && (d = d.replace("<anonymous>", e.displayName)), d;
              }
            while (1 <= o && 0 <= a);
          break;
        }
      }
    } finally {
      te = !1, Error.prepareStackTrace = n;
    }
    return (e = e ? e.displayName || e.name : "") ? C(e) : "";
  }
  function oe(e) {
    switch (e.tag) {
      case 5:
        return C(e.type);
      case 16:
        return C("Lazy");
      case 13:
        return C("Suspense");
      case 19:
        return C("SuspenseList");
      case 0:
      case 2:
      case 15:
        return e = re(e.type, !1), e;
      case 11:
        return e = re(e.type.render, !1), e;
      case 1:
        return e = re(e.type, !0), e;
      default:
        return "";
    }
  }
  function ue(e) {
    if (e == null) return null;
    if (typeof e == "function") return e.displayName || e.name || null;
    if (typeof e == "string") return e;
    switch (e) {
      case q:
        return "Fragment";
      case ge:
        return "Portal";
      case pe:
        return "Profiler";
      case D:
        return "StrictMode";
      case Fe:
        return "Suspense";
      case ct:
        return "SuspenseList";
    }
    if (typeof e == "object") switch (e.$$typeof) {
      case Oe:
        return (e.displayName || "Context") + ".Consumer";
      case ae:
        return (e._context.displayName || "Context") + ".Provider";
      case rt:
        var t = e.render;
        return e = e.displayName, e || (e = t.displayName || t.name || "", e = e !== "" ? "ForwardRef(" + e + ")" : "ForwardRef"), e;
      case gt:
        return t = e.displayName || null, t !== null ? t : ue(e.type) || "Memo";
      case Qe:
        t = e._payload, e = e._init;
        try {
          return ue(e(t));
        } catch {
        }
    }
    return null;
  }
  function he(e) {
    var t = e.type;
    switch (e.tag) {
      case 24:
        return "Cache";
      case 9:
        return (t.displayName || "Context") + ".Consumer";
      case 10:
        return (t._context.displayName || "Context") + ".Provider";
      case 18:
        return "DehydratedFragment";
      case 11:
        return e = t.render, e = e.displayName || e.name || "", t.displayName || (e !== "" ? "ForwardRef(" + e + ")" : "ForwardRef");
      case 7:
        return "Fragment";
      case 5:
        return t;
      case 4:
        return "Portal";
      case 3:
        return "Root";
      case 6:
        return "Text";
      case 16:
        return ue(t);
      case 8:
        return t === D ? "StrictMode" : "Mode";
      case 22:
        return "Offscreen";
      case 12:
        return "Profiler";
      case 21:
        return "Scope";
      case 13:
        return "Suspense";
      case 19:
        return "SuspenseList";
      case 25:
        return "TracingMarker";
      case 1:
      case 0:
      case 17:
      case 2:
      case 14:
      case 15:
        if (typeof t == "function") return t.displayName || t.name || null;
        if (typeof t == "string") return t;
    }
    return null;
  }
  function ce(e) {
    switch (typeof e) {
      case "boolean":
      case "number":
      case "string":
      case "undefined":
        return e;
      case "object":
        return e;
      default:
        return "";
    }
  }
  function we(e) {
    var t = e.type;
    return (e = e.nodeName) && e.toLowerCase() === "input" && (t === "checkbox" || t === "radio");
  }
  function Ze(e) {
    var t = we(e) ? "checked" : "value", n = Object.getOwnPropertyDescriptor(e.constructor.prototype, t), r = "" + e[t];
    if (!e.hasOwnProperty(t) && typeof n < "u" && typeof n.get == "function" && typeof n.set == "function") {
      var l = n.get, i = n.set;
      return Object.defineProperty(e, t, { configurable: !0, get: function() {
        return l.call(this);
      }, set: function(o) {
        r = "" + o, i.call(this, o);
      } }), Object.defineProperty(e, t, { enumerable: n.enumerable }), { getValue: function() {
        return r;
      }, setValue: function(o) {
        r = "" + o;
      }, stopTracking: function() {
        e._valueTracker = null, delete e[t];
      } };
    }
  }
  function Pr(e) {
    e._valueTracker || (e._valueTracker = Ze(e));
  }
  function Ho(e) {
    if (!e) return !1;
    var t = e._valueTracker;
    if (!t) return !0;
    var n = t.getValue(), r = "";
    return e && (r = we(e) ? e.checked ? "true" : "false" : e.value), e = r, e !== n ? (t.setValue(e), !0) : !1;
  }
  function Rr(e) {
    if (e = e || (typeof document < "u" ? document : void 0), typeof e > "u") return null;
    try {
      return e.activeElement || e.body;
    } catch {
      return e.body;
    }
  }
  function Al(e, t) {
    var n = t.checked;
    return B({}, t, { defaultChecked: void 0, defaultValue: void 0, value: void 0, checked: n ?? e._wrapperState.initialChecked });
  }
  function Qo(e, t) {
    var n = t.defaultValue == null ? "" : t.defaultValue, r = t.checked != null ? t.checked : t.defaultChecked;
    n = ce(t.value != null ? t.value : n), e._wrapperState = { initialChecked: r, initialValue: n, controlled: t.type === "checkbox" || t.type === "radio" ? t.checked != null : t.value != null };
  }
  function qo(e, t) {
    t = t.checked, t != null && G(e, "checked", t, !1);
  }
  function Bl(e, t) {
    qo(e, t);
    var n = ce(t.value), r = t.type;
    if (n != null) r === "number" ? (n === 0 && e.value === "" || e.value != n) && (e.value = "" + n) : e.value !== "" + n && (e.value = "" + n);
    else if (r === "submit" || r === "reset") {
      e.removeAttribute("value");
      return;
    }
    t.hasOwnProperty("value") ? Vl(e, t.type, n) : t.hasOwnProperty("defaultValue") && Vl(e, t.type, ce(t.defaultValue)), t.checked == null && t.defaultChecked != null && (e.defaultChecked = !!t.defaultChecked);
  }
  function Ko(e, t, n) {
    if (t.hasOwnProperty("value") || t.hasOwnProperty("defaultValue")) {
      var r = t.type;
      if (!(r !== "submit" && r !== "reset" || t.value !== void 0 && t.value !== null)) return;
      t = "" + e._wrapperState.initialValue, n || t === e.value || (e.value = t), e.defaultValue = t;
    }
    n = e.name, n !== "" && (e.name = ""), e.defaultChecked = !!e._wrapperState.initialChecked, n !== "" && (e.name = n);
  }
  function Vl(e, t, n) {
    (t !== "number" || Rr(e.ownerDocument) !== e) && (n == null ? e.defaultValue = "" + e._wrapperState.initialValue : e.defaultValue !== "" + n && (e.defaultValue = "" + n));
  }
  var Vn = Array.isArray;
  function vn(e, t, n, r) {
    if (e = e.options, t) {
      t = {};
      for (var l = 0; l < n.length; l++) t["$" + n[l]] = !0;
      for (n = 0; n < e.length; n++) l = t.hasOwnProperty("$" + e[n].value), e[n].selected !== l && (e[n].selected = l), l && r && (e[n].defaultSelected = !0);
    } else {
      for (n = "" + ce(n), t = null, l = 0; l < e.length; l++) {
        if (e[l].value === n) {
          e[l].selected = !0, r && (e[l].defaultSelected = !0);
          return;
        }
        t !== null || e[l].disabled || (t = e[l]);
      }
      t !== null && (t.selected = !0);
    }
  }
  function Wl(e, t) {
    if (t.dangerouslySetInnerHTML != null) throw Error(s(91));
    return B({}, t, { value: void 0, defaultValue: void 0, children: "" + e._wrapperState.initialValue });
  }
  function Yo(e, t) {
    var n = t.value;
    if (n == null) {
      if (n = t.children, t = t.defaultValue, n != null) {
        if (t != null) throw Error(s(92));
        if (Vn(n)) {
          if (1 < n.length) throw Error(s(93));
          n = n[0];
        }
        t = n;
      }
      t == null && (t = ""), n = t;
    }
    e._wrapperState = { initialValue: ce(n) };
  }
  function Xo(e, t) {
    var n = ce(t.value), r = ce(t.defaultValue);
    n != null && (n = "" + n, n !== e.value && (e.value = n), t.defaultValue == null && e.defaultValue !== n && (e.defaultValue = n)), r != null && (e.defaultValue = "" + r);
  }
  function Go(e) {
    var t = e.textContent;
    t === e._wrapperState.initialValue && t !== "" && t !== null && (e.value = t);
  }
  function Jo(e) {
    switch (e) {
      case "svg":
        return "http://www.w3.org/2000/svg";
      case "math":
        return "http://www.w3.org/1998/Math/MathML";
      default:
        return "http://www.w3.org/1999/xhtml";
    }
  }
  function Hl(e, t) {
    return e == null || e === "http://www.w3.org/1999/xhtml" ? Jo(t) : e === "http://www.w3.org/2000/svg" && t === "foreignObject" ? "http://www.w3.org/1999/xhtml" : e;
  }
  var Lr, Zo = (function(e) {
    return typeof MSApp < "u" && MSApp.execUnsafeLocalFunction ? function(t, n, r, l) {
      MSApp.execUnsafeLocalFunction(function() {
        return e(t, n, r, l);
      });
    } : e;
  })(function(e, t) {
    if (e.namespaceURI !== "http://www.w3.org/2000/svg" || "innerHTML" in e) e.innerHTML = t;
    else {
      for (Lr = Lr || document.createElement("div"), Lr.innerHTML = "<svg>" + t.valueOf().toString() + "</svg>", t = Lr.firstChild; e.firstChild; ) e.removeChild(e.firstChild);
      for (; t.firstChild; ) e.appendChild(t.firstChild);
    }
  });
  function Wn(e, t) {
    if (t) {
      var n = e.firstChild;
      if (n && n === e.lastChild && n.nodeType === 3) {
        n.nodeValue = t;
        return;
      }
    }
    e.textContent = t;
  }
  var Hn = {
    animationIterationCount: !0,
    aspectRatio: !0,
    borderImageOutset: !0,
    borderImageSlice: !0,
    borderImageWidth: !0,
    boxFlex: !0,
    boxFlexGroup: !0,
    boxOrdinalGroup: !0,
    columnCount: !0,
    columns: !0,
    flex: !0,
    flexGrow: !0,
    flexPositive: !0,
    flexShrink: !0,
    flexNegative: !0,
    flexOrder: !0,
    gridArea: !0,
    gridRow: !0,
    gridRowEnd: !0,
    gridRowSpan: !0,
    gridRowStart: !0,
    gridColumn: !0,
    gridColumnEnd: !0,
    gridColumnSpan: !0,
    gridColumnStart: !0,
    fontWeight: !0,
    lineClamp: !0,
    lineHeight: !0,
    opacity: !0,
    order: !0,
    orphans: !0,
    tabSize: !0,
    widows: !0,
    zIndex: !0,
    zoom: !0,
    fillOpacity: !0,
    floodOpacity: !0,
    stopOpacity: !0,
    strokeDasharray: !0,
    strokeDashoffset: !0,
    strokeMiterlimit: !0,
    strokeOpacity: !0,
    strokeWidth: !0
  }, dc = ["Webkit", "ms", "Moz", "O"];
  Object.keys(Hn).forEach(function(e) {
    dc.forEach(function(t) {
      t = t + e.charAt(0).toUpperCase() + e.substring(1), Hn[t] = Hn[e];
    });
  });
  function bo(e, t, n) {
    return t == null || typeof t == "boolean" || t === "" ? "" : n || typeof t != "number" || t === 0 || Hn.hasOwnProperty(e) && Hn[e] ? ("" + t).trim() : t + "px";
  }
  function eu(e, t) {
    e = e.style;
    for (var n in t) if (t.hasOwnProperty(n)) {
      var r = n.indexOf("--") === 0, l = bo(n, t[n], r);
      n === "float" && (n = "cssFloat"), r ? e.setProperty(n, l) : e[n] = l;
    }
  }
  var fc = B({ menuitem: !0 }, { area: !0, base: !0, br: !0, col: !0, embed: !0, hr: !0, img: !0, input: !0, keygen: !0, link: !0, meta: !0, param: !0, source: !0, track: !0, wbr: !0 });
  function Ql(e, t) {
    if (t) {
      if (fc[e] && (t.children != null || t.dangerouslySetInnerHTML != null)) throw Error(s(137, e));
      if (t.dangerouslySetInnerHTML != null) {
        if (t.children != null) throw Error(s(60));
        if (typeof t.dangerouslySetInnerHTML != "object" || !("__html" in t.dangerouslySetInnerHTML)) throw Error(s(61));
      }
      if (t.style != null && typeof t.style != "object") throw Error(s(62));
    }
  }
  function ql(e, t) {
    if (e.indexOf("-") === -1) return typeof t.is == "string";
    switch (e) {
      case "annotation-xml":
      case "color-profile":
      case "font-face":
      case "font-face-src":
      case "font-face-uri":
      case "font-face-format":
      case "font-face-name":
      case "missing-glyph":
        return !1;
      default:
        return !0;
    }
  }
  var Kl = null;
  function Yl(e) {
    return e = e.target || e.srcElement || window, e.correspondingUseElement && (e = e.correspondingUseElement), e.nodeType === 3 ? e.parentNode : e;
  }
  var Xl = null, yn = null, gn = null;
  function tu(e) {
    if (e = fr(e)) {
      if (typeof Xl != "function") throw Error(s(280));
      var t = e.stateNode;
      t && (t = el(t), Xl(e.stateNode, e.type, t));
    }
  }
  function nu(e) {
    yn ? gn ? gn.push(e) : gn = [e] : yn = e;
  }
  function ru() {
    if (yn) {
      var e = yn, t = gn;
      if (gn = yn = null, tu(e), t) for (e = 0; e < t.length; e++) tu(t[e]);
    }
  }
  function lu(e, t) {
    return e(t);
  }
  function iu() {
  }
  var Gl = !1;
  function ou(e, t, n) {
    if (Gl) return e(t, n);
    Gl = !0;
    try {
      return lu(e, t, n);
    } finally {
      Gl = !1, (yn !== null || gn !== null) && (iu(), ru());
    }
  }
  function Qn(e, t) {
    var n = e.stateNode;
    if (n === null) return null;
    var r = el(n);
    if (r === null) return null;
    n = r[t];
    e: switch (t) {
      case "onClick":
      case "onClickCapture":
      case "onDoubleClick":
      case "onDoubleClickCapture":
      case "onMouseDown":
      case "onMouseDownCapture":
      case "onMouseMove":
      case "onMouseMoveCapture":
      case "onMouseUp":
      case "onMouseUpCapture":
      case "onMouseEnter":
        (r = !r.disabled) || (e = e.type, r = !(e === "button" || e === "input" || e === "select" || e === "textarea")), e = !r;
        break e;
      default:
        e = !1;
    }
    if (e) return null;
    if (n && typeof n != "function") throw Error(s(231, t, typeof n));
    return n;
  }
  var Jl = !1;
  if (k) try {
    var qn = {};
    Object.defineProperty(qn, "passive", { get: function() {
      Jl = !0;
    } }), window.addEventListener("test", qn, qn), window.removeEventListener("test", qn, qn);
  } catch {
    Jl = !1;
  }
  function pc(e, t, n, r, l, i, o, a, d) {
    var w = Array.prototype.slice.call(arguments, 3);
    try {
      t.apply(n, w);
    } catch (z) {
      this.onError(z);
    }
  }
  var Kn = !1, Tr = null, Mr = !1, Zl = null, hc = { onError: function(e) {
    Kn = !0, Tr = e;
  } };
  function mc(e, t, n, r, l, i, o, a, d) {
    Kn = !1, Tr = null, pc.apply(hc, arguments);
  }
  function vc(e, t, n, r, l, i, o, a, d) {
    if (mc.apply(this, arguments), Kn) {
      if (Kn) {
        var w = Tr;
        Kn = !1, Tr = null;
      } else throw Error(s(198));
      Mr || (Mr = !0, Zl = w);
    }
  }
  function bt(e) {
    var t = e, n = e;
    if (e.alternate) for (; t.return; ) t = t.return;
    else {
      e = t;
      do
        t = e, (t.flags & 4098) !== 0 && (n = t.return), e = t.return;
      while (e);
    }
    return t.tag === 3 ? n : null;
  }
  function uu(e) {
    if (e.tag === 13) {
      var t = e.memoizedState;
      if (t === null && (e = e.alternate, e !== null && (t = e.memoizedState)), t !== null) return t.dehydrated;
    }
    return null;
  }
  function su(e) {
    if (bt(e) !== e) throw Error(s(188));
  }
  function yc(e) {
    var t = e.alternate;
    if (!t) {
      if (t = bt(e), t === null) throw Error(s(188));
      return t !== e ? null : e;
    }
    for (var n = e, r = t; ; ) {
      var l = n.return;
      if (l === null) break;
      var i = l.alternate;
      if (i === null) {
        if (r = l.return, r !== null) {
          n = r;
          continue;
        }
        break;
      }
      if (l.child === i.child) {
        for (i = l.child; i; ) {
          if (i === n) return su(l), e;
          if (i === r) return su(l), t;
          i = i.sibling;
        }
        throw Error(s(188));
      }
      if (n.return !== r.return) n = l, r = i;
      else {
        for (var o = !1, a = l.child; a; ) {
          if (a === n) {
            o = !0, n = l, r = i;
            break;
          }
          if (a === r) {
            o = !0, r = l, n = i;
            break;
          }
          a = a.sibling;
        }
        if (!o) {
          for (a = i.child; a; ) {
            if (a === n) {
              o = !0, n = i, r = l;
              break;
            }
            if (a === r) {
              o = !0, r = i, n = l;
              break;
            }
            a = a.sibling;
          }
          if (!o) throw Error(s(189));
        }
      }
      if (n.alternate !== r) throw Error(s(190));
    }
    if (n.tag !== 3) throw Error(s(188));
    return n.stateNode.current === n ? e : t;
  }
  function au(e) {
    return e = yc(e), e !== null ? cu(e) : null;
  }
  function cu(e) {
    if (e.tag === 5 || e.tag === 6) return e;
    for (e = e.child; e !== null; ) {
      var t = cu(e);
      if (t !== null) return t;
      e = e.sibling;
    }
    return null;
  }
  var du = f.unstable_scheduleCallback, fu = f.unstable_cancelCallback, gc = f.unstable_shouldYield, wc = f.unstable_requestPaint, Ne = f.unstable_now, xc = f.unstable_getCurrentPriorityLevel, bl = f.unstable_ImmediatePriority, pu = f.unstable_UserBlockingPriority, Ir = f.unstable_NormalPriority, kc = f.unstable_LowPriority, hu = f.unstable_IdlePriority, Dr = null, wt = null;
  function Sc(e) {
    if (wt && typeof wt.onCommitFiberRoot == "function") try {
      wt.onCommitFiberRoot(Dr, e, void 0, (e.current.flags & 128) === 128);
    } catch {
    }
  }
  var dt = Math.clz32 ? Math.clz32 : Nc, _c = Math.log, jc = Math.LN2;
  function Nc(e) {
    return e >>>= 0, e === 0 ? 32 : 31 - (_c(e) / jc | 0) | 0;
  }
  var Or = 64, Fr = 4194304;
  function Yn(e) {
    switch (e & -e) {
      case 1:
        return 1;
      case 2:
        return 2;
      case 4:
        return 4;
      case 8:
        return 8;
      case 16:
        return 16;
      case 32:
        return 32;
      case 64:
      case 128:
      case 256:
      case 512:
      case 1024:
      case 2048:
      case 4096:
      case 8192:
      case 16384:
      case 32768:
      case 65536:
      case 131072:
      case 262144:
      case 524288:
      case 1048576:
      case 2097152:
        return e & 4194240;
      case 4194304:
      case 8388608:
      case 16777216:
      case 33554432:
      case 67108864:
        return e & 130023424;
      case 134217728:
        return 134217728;
      case 268435456:
        return 268435456;
      case 536870912:
        return 536870912;
      case 1073741824:
        return 1073741824;
      default:
        return e;
    }
  }
  function $r(e, t) {
    var n = e.pendingLanes;
    if (n === 0) return 0;
    var r = 0, l = e.suspendedLanes, i = e.pingedLanes, o = n & 268435455;
    if (o !== 0) {
      var a = o & ~l;
      a !== 0 ? r = Yn(a) : (i &= o, i !== 0 && (r = Yn(i)));
    } else o = n & ~l, o !== 0 ? r = Yn(o) : i !== 0 && (r = Yn(i));
    if (r === 0) return 0;
    if (t !== 0 && t !== r && (t & l) === 0 && (l = r & -r, i = t & -t, l >= i || l === 16 && (i & 4194240) !== 0)) return t;
    if ((r & 4) !== 0 && (r |= n & 16), t = e.entangledLanes, t !== 0) for (e = e.entanglements, t &= r; 0 < t; ) n = 31 - dt(t), l = 1 << n, r |= e[n], t &= ~l;
    return r;
  }
  function Ec(e, t) {
    switch (e) {
      case 1:
      case 2:
      case 4:
        return t + 250;
      case 8:
      case 16:
      case 32:
      case 64:
      case 128:
      case 256:
      case 512:
      case 1024:
      case 2048:
      case 4096:
      case 8192:
      case 16384:
      case 32768:
      case 65536:
      case 131072:
      case 262144:
      case 524288:
      case 1048576:
      case 2097152:
        return t + 5e3;
      case 4194304:
      case 8388608:
      case 16777216:
      case 33554432:
      case 67108864:
        return -1;
      case 134217728:
      case 268435456:
      case 536870912:
      case 1073741824:
        return -1;
      default:
        return -1;
    }
  }
  function Cc(e, t) {
    for (var n = e.suspendedLanes, r = e.pingedLanes, l = e.expirationTimes, i = e.pendingLanes; 0 < i; ) {
      var o = 31 - dt(i), a = 1 << o, d = l[o];
      d === -1 ? ((a & n) === 0 || (a & r) !== 0) && (l[o] = Ec(a, t)) : d <= t && (e.expiredLanes |= a), i &= ~a;
    }
  }
  function ei(e) {
    return e = e.pendingLanes & -1073741825, e !== 0 ? e : e & 1073741824 ? 1073741824 : 0;
  }
  function mu() {
    var e = Or;
    return Or <<= 1, (Or & 4194240) === 0 && (Or = 64), e;
  }
  function ti(e) {
    for (var t = [], n = 0; 31 > n; n++) t.push(e);
    return t;
  }
  function Xn(e, t, n) {
    e.pendingLanes |= t, t !== 536870912 && (e.suspendedLanes = 0, e.pingedLanes = 0), e = e.eventTimes, t = 31 - dt(t), e[t] = n;
  }
  function zc(e, t) {
    var n = e.pendingLanes & ~t;
    e.pendingLanes = t, e.suspendedLanes = 0, e.pingedLanes = 0, e.expiredLanes &= t, e.mutableReadLanes &= t, e.entangledLanes &= t, t = e.entanglements;
    var r = e.eventTimes;
    for (e = e.expirationTimes; 0 < n; ) {
      var l = 31 - dt(n), i = 1 << l;
      t[l] = 0, r[l] = -1, e[l] = -1, n &= ~i;
    }
  }
  function ni(e, t) {
    var n = e.entangledLanes |= t;
    for (e = e.entanglements; n; ) {
      var r = 31 - dt(n), l = 1 << r;
      l & t | e[r] & t && (e[r] |= t), n &= ~l;
    }
  }
  var de = 0;
  function vu(e) {
    return e &= -e, 1 < e ? 4 < e ? (e & 268435455) !== 0 ? 16 : 536870912 : 4 : 1;
  }
  var yu, ri, gu, wu, xu, li = !1, Ur = [], Mt = null, It = null, Dt = null, Gn = /* @__PURE__ */ new Map(), Jn = /* @__PURE__ */ new Map(), Ot = [], Pc = "mousedown mouseup touchcancel touchend touchstart auxclick dblclick pointercancel pointerdown pointerup dragend dragstart drop compositionend compositionstart keydown keypress keyup input textInput copy cut paste click change contextmenu reset submit".split(" ");
  function ku(e, t) {
    switch (e) {
      case "focusin":
      case "focusout":
        Mt = null;
        break;
      case "dragenter":
      case "dragleave":
        It = null;
        break;
      case "mouseover":
      case "mouseout":
        Dt = null;
        break;
      case "pointerover":
      case "pointerout":
        Gn.delete(t.pointerId);
        break;
      case "gotpointercapture":
      case "lostpointercapture":
        Jn.delete(t.pointerId);
    }
  }
  function Zn(e, t, n, r, l, i) {
    return e === null || e.nativeEvent !== i ? (e = { blockedOn: t, domEventName: n, eventSystemFlags: r, nativeEvent: i, targetContainers: [l] }, t !== null && (t = fr(t), t !== null && ri(t)), e) : (e.eventSystemFlags |= r, t = e.targetContainers, l !== null && t.indexOf(l) === -1 && t.push(l), e);
  }
  function Rc(e, t, n, r, l) {
    switch (t) {
      case "focusin":
        return Mt = Zn(Mt, e, t, n, r, l), !0;
      case "dragenter":
        return It = Zn(It, e, t, n, r, l), !0;
      case "mouseover":
        return Dt = Zn(Dt, e, t, n, r, l), !0;
      case "pointerover":
        var i = l.pointerId;
        return Gn.set(i, Zn(Gn.get(i) || null, e, t, n, r, l)), !0;
      case "gotpointercapture":
        return i = l.pointerId, Jn.set(i, Zn(Jn.get(i) || null, e, t, n, r, l)), !0;
    }
    return !1;
  }
  function Su(e) {
    var t = en(e.target);
    if (t !== null) {
      var n = bt(t);
      if (n !== null) {
        if (t = n.tag, t === 13) {
          if (t = uu(n), t !== null) {
            e.blockedOn = t, xu(e.priority, function() {
              gu(n);
            });
            return;
          }
        } else if (t === 3 && n.stateNode.current.memoizedState.isDehydrated) {
          e.blockedOn = n.tag === 3 ? n.stateNode.containerInfo : null;
          return;
        }
      }
    }
    e.blockedOn = null;
  }
  function Ar(e) {
    if (e.blockedOn !== null) return !1;
    for (var t = e.targetContainers; 0 < t.length; ) {
      var n = oi(e.domEventName, e.eventSystemFlags, t[0], e.nativeEvent);
      if (n === null) {
        n = e.nativeEvent;
        var r = new n.constructor(n.type, n);
        Kl = r, n.target.dispatchEvent(r), Kl = null;
      } else return t = fr(n), t !== null && ri(t), e.blockedOn = n, !1;
      t.shift();
    }
    return !0;
  }
  function _u(e, t, n) {
    Ar(e) && n.delete(t);
  }
  function Lc() {
    li = !1, Mt !== null && Ar(Mt) && (Mt = null), It !== null && Ar(It) && (It = null), Dt !== null && Ar(Dt) && (Dt = null), Gn.forEach(_u), Jn.forEach(_u);
  }
  function bn(e, t) {
    e.blockedOn === t && (e.blockedOn = null, li || (li = !0, f.unstable_scheduleCallback(f.unstable_NormalPriority, Lc)));
  }
  function er(e) {
    function t(l) {
      return bn(l, e);
    }
    if (0 < Ur.length) {
      bn(Ur[0], e);
      for (var n = 1; n < Ur.length; n++) {
        var r = Ur[n];
        r.blockedOn === e && (r.blockedOn = null);
      }
    }
    for (Mt !== null && bn(Mt, e), It !== null && bn(It, e), Dt !== null && bn(Dt, e), Gn.forEach(t), Jn.forEach(t), n = 0; n < Ot.length; n++) r = Ot[n], r.blockedOn === e && (r.blockedOn = null);
    for (; 0 < Ot.length && (n = Ot[0], n.blockedOn === null); ) Su(n), n.blockedOn === null && Ot.shift();
  }
  var wn = ee.ReactCurrentBatchConfig, Br = !0;
  function Tc(e, t, n, r) {
    var l = de, i = wn.transition;
    wn.transition = null;
    try {
      de = 1, ii(e, t, n, r);
    } finally {
      de = l, wn.transition = i;
    }
  }
  function Mc(e, t, n, r) {
    var l = de, i = wn.transition;
    wn.transition = null;
    try {
      de = 4, ii(e, t, n, r);
    } finally {
      de = l, wn.transition = i;
    }
  }
  function ii(e, t, n, r) {
    if (Br) {
      var l = oi(e, t, n, r);
      if (l === null) _i(e, t, r, Vr, n), ku(e, r);
      else if (Rc(l, e, t, n, r)) r.stopPropagation();
      else if (ku(e, r), t & 4 && -1 < Pc.indexOf(e)) {
        for (; l !== null; ) {
          var i = fr(l);
          if (i !== null && yu(i), i = oi(e, t, n, r), i === null && _i(e, t, r, Vr, n), i === l) break;
          l = i;
        }
        l !== null && r.stopPropagation();
      } else _i(e, t, r, null, n);
    }
  }
  var Vr = null;
  function oi(e, t, n, r) {
    if (Vr = null, e = Yl(r), e = en(e), e !== null) if (t = bt(e), t === null) e = null;
    else if (n = t.tag, n === 13) {
      if (e = uu(t), e !== null) return e;
      e = null;
    } else if (n === 3) {
      if (t.stateNode.current.memoizedState.isDehydrated) return t.tag === 3 ? t.stateNode.containerInfo : null;
      e = null;
    } else t !== e && (e = null);
    return Vr = e, null;
  }
  function ju(e) {
    switch (e) {
      case "cancel":
      case "click":
      case "close":
      case "contextmenu":
      case "copy":
      case "cut":
      case "auxclick":
      case "dblclick":
      case "dragend":
      case "dragstart":
      case "drop":
      case "focusin":
      case "focusout":
      case "input":
      case "invalid":
      case "keydown":
      case "keypress":
      case "keyup":
      case "mousedown":
      case "mouseup":
      case "paste":
      case "pause":
      case "play":
      case "pointercancel":
      case "pointerdown":
      case "pointerup":
      case "ratechange":
      case "reset":
      case "resize":
      case "seeked":
      case "submit":
      case "touchcancel":
      case "touchend":
      case "touchstart":
      case "volumechange":
      case "change":
      case "selectionchange":
      case "textInput":
      case "compositionstart":
      case "compositionend":
      case "compositionupdate":
      case "beforeblur":
      case "afterblur":
      case "beforeinput":
      case "blur":
      case "fullscreenchange":
      case "focus":
      case "hashchange":
      case "popstate":
      case "select":
      case "selectstart":
        return 1;
      case "drag":
      case "dragenter":
      case "dragexit":
      case "dragleave":
      case "dragover":
      case "mousemove":
      case "mouseout":
      case "mouseover":
      case "pointermove":
      case "pointerout":
      case "pointerover":
      case "scroll":
      case "toggle":
      case "touchmove":
      case "wheel":
      case "mouseenter":
      case "mouseleave":
      case "pointerenter":
      case "pointerleave":
        return 4;
      case "message":
        switch (xc()) {
          case bl:
            return 1;
          case pu:
            return 4;
          case Ir:
          case kc:
            return 16;
          case hu:
            return 536870912;
          default:
            return 16;
        }
      default:
        return 16;
    }
  }
  var Ft = null, ui = null, Wr = null;
  function Nu() {
    if (Wr) return Wr;
    var e, t = ui, n = t.length, r, l = "value" in Ft ? Ft.value : Ft.textContent, i = l.length;
    for (e = 0; e < n && t[e] === l[e]; e++) ;
    var o = n - e;
    for (r = 1; r <= o && t[n - r] === l[i - r]; r++) ;
    return Wr = l.slice(e, 1 < r ? 1 - r : void 0);
  }
  function Hr(e) {
    var t = e.keyCode;
    return "charCode" in e ? (e = e.charCode, e === 0 && t === 13 && (e = 13)) : e = t, e === 10 && (e = 13), 32 <= e || e === 13 ? e : 0;
  }
  function Qr() {
    return !0;
  }
  function Eu() {
    return !1;
  }
  function be(e) {
    function t(n, r, l, i, o) {
      this._reactName = n, this._targetInst = l, this.type = r, this.nativeEvent = i, this.target = o, this.currentTarget = null;
      for (var a in e) e.hasOwnProperty(a) && (n = e[a], this[a] = n ? n(i) : i[a]);
      return this.isDefaultPrevented = (i.defaultPrevented != null ? i.defaultPrevented : i.returnValue === !1) ? Qr : Eu, this.isPropagationStopped = Eu, this;
    }
    return B(t.prototype, { preventDefault: function() {
      this.defaultPrevented = !0;
      var n = this.nativeEvent;
      n && (n.preventDefault ? n.preventDefault() : typeof n.returnValue != "unknown" && (n.returnValue = !1), this.isDefaultPrevented = Qr);
    }, stopPropagation: function() {
      var n = this.nativeEvent;
      n && (n.stopPropagation ? n.stopPropagation() : typeof n.cancelBubble != "unknown" && (n.cancelBubble = !0), this.isPropagationStopped = Qr);
    }, persist: function() {
    }, isPersistent: Qr }), t;
  }
  var xn = { eventPhase: 0, bubbles: 0, cancelable: 0, timeStamp: function(e) {
    return e.timeStamp || Date.now();
  }, defaultPrevented: 0, isTrusted: 0 }, si = be(xn), tr = B({}, xn, { view: 0, detail: 0 }), Ic = be(tr), ai, ci, nr, qr = B({}, tr, { screenX: 0, screenY: 0, clientX: 0, clientY: 0, pageX: 0, pageY: 0, ctrlKey: 0, shiftKey: 0, altKey: 0, metaKey: 0, getModifierState: fi, button: 0, buttons: 0, relatedTarget: function(e) {
    return e.relatedTarget === void 0 ? e.fromElement === e.srcElement ? e.toElement : e.fromElement : e.relatedTarget;
  }, movementX: function(e) {
    return "movementX" in e ? e.movementX : (e !== nr && (nr && e.type === "mousemove" ? (ai = e.screenX - nr.screenX, ci = e.screenY - nr.screenY) : ci = ai = 0, nr = e), ai);
  }, movementY: function(e) {
    return "movementY" in e ? e.movementY : ci;
  } }), Cu = be(qr), Dc = B({}, qr, { dataTransfer: 0 }), Oc = be(Dc), Fc = B({}, tr, { relatedTarget: 0 }), di = be(Fc), $c = B({}, xn, { animationName: 0, elapsedTime: 0, pseudoElement: 0 }), Uc = be($c), Ac = B({}, xn, { clipboardData: function(e) {
    return "clipboardData" in e ? e.clipboardData : window.clipboardData;
  } }), Bc = be(Ac), Vc = B({}, xn, { data: 0 }), zu = be(Vc), Wc = {
    Esc: "Escape",
    Spacebar: " ",
    Left: "ArrowLeft",
    Up: "ArrowUp",
    Right: "ArrowRight",
    Down: "ArrowDown",
    Del: "Delete",
    Win: "OS",
    Menu: "ContextMenu",
    Apps: "ContextMenu",
    Scroll: "ScrollLock",
    MozPrintableKey: "Unidentified"
  }, Hc = {
    8: "Backspace",
    9: "Tab",
    12: "Clear",
    13: "Enter",
    16: "Shift",
    17: "Control",
    18: "Alt",
    19: "Pause",
    20: "CapsLock",
    27: "Escape",
    32: " ",
    33: "PageUp",
    34: "PageDown",
    35: "End",
    36: "Home",
    37: "ArrowLeft",
    38: "ArrowUp",
    39: "ArrowRight",
    40: "ArrowDown",
    45: "Insert",
    46: "Delete",
    112: "F1",
    113: "F2",
    114: "F3",
    115: "F4",
    116: "F5",
    117: "F6",
    118: "F7",
    119: "F8",
    120: "F9",
    121: "F10",
    122: "F11",
    123: "F12",
    144: "NumLock",
    145: "ScrollLock",
    224: "Meta"
  }, Qc = { Alt: "altKey", Control: "ctrlKey", Meta: "metaKey", Shift: "shiftKey" };
  function qc(e) {
    var t = this.nativeEvent;
    return t.getModifierState ? t.getModifierState(e) : (e = Qc[e]) ? !!t[e] : !1;
  }
  function fi() {
    return qc;
  }
  var Kc = B({}, tr, { key: function(e) {
    if (e.key) {
      var t = Wc[e.key] || e.key;
      if (t !== "Unidentified") return t;
    }
    return e.type === "keypress" ? (e = Hr(e), e === 13 ? "Enter" : String.fromCharCode(e)) : e.type === "keydown" || e.type === "keyup" ? Hc[e.keyCode] || "Unidentified" : "";
  }, code: 0, location: 0, ctrlKey: 0, shiftKey: 0, altKey: 0, metaKey: 0, repeat: 0, locale: 0, getModifierState: fi, charCode: function(e) {
    return e.type === "keypress" ? Hr(e) : 0;
  }, keyCode: function(e) {
    return e.type === "keydown" || e.type === "keyup" ? e.keyCode : 0;
  }, which: function(e) {
    return e.type === "keypress" ? Hr(e) : e.type === "keydown" || e.type === "keyup" ? e.keyCode : 0;
  } }), Yc = be(Kc), Xc = B({}, qr, { pointerId: 0, width: 0, height: 0, pressure: 0, tangentialPressure: 0, tiltX: 0, tiltY: 0, twist: 0, pointerType: 0, isPrimary: 0 }), Pu = be(Xc), Gc = B({}, tr, { touches: 0, targetTouches: 0, changedTouches: 0, altKey: 0, metaKey: 0, ctrlKey: 0, shiftKey: 0, getModifierState: fi }), Jc = be(Gc), Zc = B({}, xn, { propertyName: 0, elapsedTime: 0, pseudoElement: 0 }), bc = be(Zc), ed = B({}, qr, {
    deltaX: function(e) {
      return "deltaX" in e ? e.deltaX : "wheelDeltaX" in e ? -e.wheelDeltaX : 0;
    },
    deltaY: function(e) {
      return "deltaY" in e ? e.deltaY : "wheelDeltaY" in e ? -e.wheelDeltaY : "wheelDelta" in e ? -e.wheelDelta : 0;
    },
    deltaZ: 0,
    deltaMode: 0
  }), td = be(ed), nd = [9, 13, 27, 32], pi = k && "CompositionEvent" in window, rr = null;
  k && "documentMode" in document && (rr = document.documentMode);
  var rd = k && "TextEvent" in window && !rr, Ru = k && (!pi || rr && 8 < rr && 11 >= rr), Lu = " ", Tu = !1;
  function Mu(e, t) {
    switch (e) {
      case "keyup":
        return nd.indexOf(t.keyCode) !== -1;
      case "keydown":
        return t.keyCode !== 229;
      case "keypress":
      case "mousedown":
      case "focusout":
        return !0;
      default:
        return !1;
    }
  }
  function Iu(e) {
    return e = e.detail, typeof e == "object" && "data" in e ? e.data : null;
  }
  var kn = !1;
  function ld(e, t) {
    switch (e) {
      case "compositionend":
        return Iu(t);
      case "keypress":
        return t.which !== 32 ? null : (Tu = !0, Lu);
      case "textInput":
        return e = t.data, e === Lu && Tu ? null : e;
      default:
        return null;
    }
  }
  function id(e, t) {
    if (kn) return e === "compositionend" || !pi && Mu(e, t) ? (e = Nu(), Wr = ui = Ft = null, kn = !1, e) : null;
    switch (e) {
      case "paste":
        return null;
      case "keypress":
        if (!(t.ctrlKey || t.altKey || t.metaKey) || t.ctrlKey && t.altKey) {
          if (t.char && 1 < t.char.length) return t.char;
          if (t.which) return String.fromCharCode(t.which);
        }
        return null;
      case "compositionend":
        return Ru && t.locale !== "ko" ? null : t.data;
      default:
        return null;
    }
  }
  var od = { color: !0, date: !0, datetime: !0, "datetime-local": !0, email: !0, month: !0, number: !0, password: !0, range: !0, search: !0, tel: !0, text: !0, time: !0, url: !0, week: !0 };
  function Du(e) {
    var t = e && e.nodeName && e.nodeName.toLowerCase();
    return t === "input" ? !!od[e.type] : t === "textarea";
  }
  function Ou(e, t, n, r) {
    nu(r), t = Jr(t, "onChange"), 0 < t.length && (n = new si("onChange", "change", null, n, r), e.push({ event: n, listeners: t }));
  }
  var lr = null, ir = null;
  function ud(e) {
    es(e, 0);
  }
  function Kr(e) {
    var t = En(e);
    if (Ho(t)) return e;
  }
  function sd(e, t) {
    if (e === "change") return t;
  }
  var Fu = !1;
  if (k) {
    var hi;
    if (k) {
      var mi = "oninput" in document;
      if (!mi) {
        var $u = document.createElement("div");
        $u.setAttribute("oninput", "return;"), mi = typeof $u.oninput == "function";
      }
      hi = mi;
    } else hi = !1;
    Fu = hi && (!document.documentMode || 9 < document.documentMode);
  }
  function Uu() {
    lr && (lr.detachEvent("onpropertychange", Au), ir = lr = null);
  }
  function Au(e) {
    if (e.propertyName === "value" && Kr(ir)) {
      var t = [];
      Ou(t, ir, e, Yl(e)), ou(ud, t);
    }
  }
  function ad(e, t, n) {
    e === "focusin" ? (Uu(), lr = t, ir = n, lr.attachEvent("onpropertychange", Au)) : e === "focusout" && Uu();
  }
  function cd(e) {
    if (e === "selectionchange" || e === "keyup" || e === "keydown") return Kr(ir);
  }
  function dd(e, t) {
    if (e === "click") return Kr(t);
  }
  function fd(e, t) {
    if (e === "input" || e === "change") return Kr(t);
  }
  function pd(e, t) {
    return e === t && (e !== 0 || 1 / e === 1 / t) || e !== e && t !== t;
  }
  var ft = typeof Object.is == "function" ? Object.is : pd;
  function or(e, t) {
    if (ft(e, t)) return !0;
    if (typeof e != "object" || e === null || typeof t != "object" || t === null) return !1;
    var n = Object.keys(e), r = Object.keys(t);
    if (n.length !== r.length) return !1;
    for (r = 0; r < n.length; r++) {
      var l = n[r];
      if (!M.call(t, l) || !ft(e[l], t[l])) return !1;
    }
    return !0;
  }
  function Bu(e) {
    for (; e && e.firstChild; ) e = e.firstChild;
    return e;
  }
  function Vu(e, t) {
    var n = Bu(e);
    e = 0;
    for (var r; n; ) {
      if (n.nodeType === 3) {
        if (r = e + n.textContent.length, e <= t && r >= t) return { node: n, offset: t - e };
        e = r;
      }
      e: {
        for (; n; ) {
          if (n.nextSibling) {
            n = n.nextSibling;
            break e;
          }
          n = n.parentNode;
        }
        n = void 0;
      }
      n = Bu(n);
    }
  }
  function Wu(e, t) {
    return e && t ? e === t ? !0 : e && e.nodeType === 3 ? !1 : t && t.nodeType === 3 ? Wu(e, t.parentNode) : "contains" in e ? e.contains(t) : e.compareDocumentPosition ? !!(e.compareDocumentPosition(t) & 16) : !1 : !1;
  }
  function Hu() {
    for (var e = window, t = Rr(); t instanceof e.HTMLIFrameElement; ) {
      try {
        var n = typeof t.contentWindow.location.href == "string";
      } catch {
        n = !1;
      }
      if (n) e = t.contentWindow;
      else break;
      t = Rr(e.document);
    }
    return t;
  }
  function vi(e) {
    var t = e && e.nodeName && e.nodeName.toLowerCase();
    return t && (t === "input" && (e.type === "text" || e.type === "search" || e.type === "tel" || e.type === "url" || e.type === "password") || t === "textarea" || e.contentEditable === "true");
  }
  function hd(e) {
    var t = Hu(), n = e.focusedElem, r = e.selectionRange;
    if (t !== n && n && n.ownerDocument && Wu(n.ownerDocument.documentElement, n)) {
      if (r !== null && vi(n)) {
        if (t = r.start, e = r.end, e === void 0 && (e = t), "selectionStart" in n) n.selectionStart = t, n.selectionEnd = Math.min(e, n.value.length);
        else if (e = (t = n.ownerDocument || document) && t.defaultView || window, e.getSelection) {
          e = e.getSelection();
          var l = n.textContent.length, i = Math.min(r.start, l);
          r = r.end === void 0 ? i : Math.min(r.end, l), !e.extend && i > r && (l = r, r = i, i = l), l = Vu(n, i);
          var o = Vu(
            n,
            r
          );
          l && o && (e.rangeCount !== 1 || e.anchorNode !== l.node || e.anchorOffset !== l.offset || e.focusNode !== o.node || e.focusOffset !== o.offset) && (t = t.createRange(), t.setStart(l.node, l.offset), e.removeAllRanges(), i > r ? (e.addRange(t), e.extend(o.node, o.offset)) : (t.setEnd(o.node, o.offset), e.addRange(t)));
        }
      }
      for (t = [], e = n; e = e.parentNode; ) e.nodeType === 1 && t.push({ element: e, left: e.scrollLeft, top: e.scrollTop });
      for (typeof n.focus == "function" && n.focus(), n = 0; n < t.length; n++) e = t[n], e.element.scrollLeft = e.left, e.element.scrollTop = e.top;
    }
  }
  var md = k && "documentMode" in document && 11 >= document.documentMode, Sn = null, yi = null, ur = null, gi = !1;
  function Qu(e, t, n) {
    var r = n.window === n ? n.document : n.nodeType === 9 ? n : n.ownerDocument;
    gi || Sn == null || Sn !== Rr(r) || (r = Sn, "selectionStart" in r && vi(r) ? r = { start: r.selectionStart, end: r.selectionEnd } : (r = (r.ownerDocument && r.ownerDocument.defaultView || window).getSelection(), r = { anchorNode: r.anchorNode, anchorOffset: r.anchorOffset, focusNode: r.focusNode, focusOffset: r.focusOffset }), ur && or(ur, r) || (ur = r, r = Jr(yi, "onSelect"), 0 < r.length && (t = new si("onSelect", "select", null, t, n), e.push({ event: t, listeners: r }), t.target = Sn)));
  }
  function Yr(e, t) {
    var n = {};
    return n[e.toLowerCase()] = t.toLowerCase(), n["Webkit" + e] = "webkit" + t, n["Moz" + e] = "moz" + t, n;
  }
  var _n = { animationend: Yr("Animation", "AnimationEnd"), animationiteration: Yr("Animation", "AnimationIteration"), animationstart: Yr("Animation", "AnimationStart"), transitionend: Yr("Transition", "TransitionEnd") }, wi = {}, qu = {};
  k && (qu = document.createElement("div").style, "AnimationEvent" in window || (delete _n.animationend.animation, delete _n.animationiteration.animation, delete _n.animationstart.animation), "TransitionEvent" in window || delete _n.transitionend.transition);
  function Xr(e) {
    if (wi[e]) return wi[e];
    if (!_n[e]) return e;
    var t = _n[e], n;
    for (n in t) if (t.hasOwnProperty(n) && n in qu) return wi[e] = t[n];
    return e;
  }
  var Ku = Xr("animationend"), Yu = Xr("animationiteration"), Xu = Xr("animationstart"), Gu = Xr("transitionend"), Ju = /* @__PURE__ */ new Map(), Zu = "abort auxClick cancel canPlay canPlayThrough click close contextMenu copy cut drag dragEnd dragEnter dragExit dragLeave dragOver dragStart drop durationChange emptied encrypted ended error gotPointerCapture input invalid keyDown keyPress keyUp load loadedData loadedMetadata loadStart lostPointerCapture mouseDown mouseMove mouseOut mouseOver mouseUp paste pause play playing pointerCancel pointerDown pointerMove pointerOut pointerOver pointerUp progress rateChange reset resize seeked seeking stalled submit suspend timeUpdate touchCancel touchEnd touchStart volumeChange scroll toggle touchMove waiting wheel".split(" ");
  function $t(e, t) {
    Ju.set(e, t), x(t, [e]);
  }
  for (var xi = 0; xi < Zu.length; xi++) {
    var ki = Zu[xi], vd = ki.toLowerCase(), yd = ki[0].toUpperCase() + ki.slice(1);
    $t(vd, "on" + yd);
  }
  $t(Ku, "onAnimationEnd"), $t(Yu, "onAnimationIteration"), $t(Xu, "onAnimationStart"), $t("dblclick", "onDoubleClick"), $t("focusin", "onFocus"), $t("focusout", "onBlur"), $t(Gu, "onTransitionEnd"), _("onMouseEnter", ["mouseout", "mouseover"]), _("onMouseLeave", ["mouseout", "mouseover"]), _("onPointerEnter", ["pointerout", "pointerover"]), _("onPointerLeave", ["pointerout", "pointerover"]), x("onChange", "change click focusin focusout input keydown keyup selectionchange".split(" ")), x("onSelect", "focusout contextmenu dragend focusin keydown keyup mousedown mouseup selectionchange".split(" ")), x("onBeforeInput", ["compositionend", "keypress", "textInput", "paste"]), x("onCompositionEnd", "compositionend focusout keydown keypress keyup mousedown".split(" ")), x("onCompositionStart", "compositionstart focusout keydown keypress keyup mousedown".split(" ")), x("onCompositionUpdate", "compositionupdate focusout keydown keypress keyup mousedown".split(" "));
  var sr = "abort canplay canplaythrough durationchange emptied encrypted ended error loadeddata loadedmetadata loadstart pause play playing progress ratechange resize seeked seeking stalled suspend timeupdate volumechange waiting".split(" "), gd = new Set("cancel close invalid load scroll toggle".split(" ").concat(sr));
  function bu(e, t, n) {
    var r = e.type || "unknown-event";
    e.currentTarget = n, vc(r, t, void 0, e), e.currentTarget = null;
  }
  function es(e, t) {
    t = (t & 4) !== 0;
    for (var n = 0; n < e.length; n++) {
      var r = e[n], l = r.event;
      r = r.listeners;
      e: {
        var i = void 0;
        if (t) for (var o = r.length - 1; 0 <= o; o--) {
          var a = r[o], d = a.instance, w = a.currentTarget;
          if (a = a.listener, d !== i && l.isPropagationStopped()) break e;
          bu(l, a, w), i = d;
        }
        else for (o = 0; o < r.length; o++) {
          if (a = r[o], d = a.instance, w = a.currentTarget, a = a.listener, d !== i && l.isPropagationStopped()) break e;
          bu(l, a, w), i = d;
        }
      }
    }
    if (Mr) throw e = Zl, Mr = !1, Zl = null, e;
  }
  function ve(e, t) {
    var n = t[Pi];
    n === void 0 && (n = t[Pi] = /* @__PURE__ */ new Set());
    var r = e + "__bubble";
    n.has(r) || (ts(t, e, 2, !1), n.add(r));
  }
  function Si(e, t, n) {
    var r = 0;
    t && (r |= 4), ts(n, e, r, t);
  }
  var Gr = "_reactListening" + Math.random().toString(36).slice(2);
  function ar(e) {
    if (!e[Gr]) {
      e[Gr] = !0, y.forEach(function(n) {
        n !== "selectionchange" && (gd.has(n) || Si(n, !1, e), Si(n, !0, e));
      });
      var t = e.nodeType === 9 ? e : e.ownerDocument;
      t === null || t[Gr] || (t[Gr] = !0, Si("selectionchange", !1, t));
    }
  }
  function ts(e, t, n, r) {
    switch (ju(t)) {
      case 1:
        var l = Tc;
        break;
      case 4:
        l = Mc;
        break;
      default:
        l = ii;
    }
    n = l.bind(null, t, n, e), l = void 0, !Jl || t !== "touchstart" && t !== "touchmove" && t !== "wheel" || (l = !0), r ? l !== void 0 ? e.addEventListener(t, n, { capture: !0, passive: l }) : e.addEventListener(t, n, !0) : l !== void 0 ? e.addEventListener(t, n, { passive: l }) : e.addEventListener(t, n, !1);
  }
  function _i(e, t, n, r, l) {
    var i = r;
    if ((t & 1) === 0 && (t & 2) === 0 && r !== null) e: for (; ; ) {
      if (r === null) return;
      var o = r.tag;
      if (o === 3 || o === 4) {
        var a = r.stateNode.containerInfo;
        if (a === l || a.nodeType === 8 && a.parentNode === l) break;
        if (o === 4) for (o = r.return; o !== null; ) {
          var d = o.tag;
          if ((d === 3 || d === 4) && (d = o.stateNode.containerInfo, d === l || d.nodeType === 8 && d.parentNode === l)) return;
          o = o.return;
        }
        for (; a !== null; ) {
          if (o = en(a), o === null) return;
          if (d = o.tag, d === 5 || d === 6) {
            r = i = o;
            continue e;
          }
          a = a.parentNode;
        }
      }
      r = r.return;
    }
    ou(function() {
      var w = i, z = Yl(n), L = [];
      e: {
        var j = Ju.get(e);
        if (j !== void 0) {
          var U = si, V = e;
          switch (e) {
            case "keypress":
              if (Hr(n) === 0) break e;
            case "keydown":
            case "keyup":
              U = Yc;
              break;
            case "focusin":
              V = "focus", U = di;
              break;
            case "focusout":
              V = "blur", U = di;
              break;
            case "beforeblur":
            case "afterblur":
              U = di;
              break;
            case "click":
              if (n.button === 2) break e;
            case "auxclick":
            case "dblclick":
            case "mousedown":
            case "mousemove":
            case "mouseup":
            case "mouseout":
            case "mouseover":
            case "contextmenu":
              U = Cu;
              break;
            case "drag":
            case "dragend":
            case "dragenter":
            case "dragexit":
            case "dragleave":
            case "dragover":
            case "dragstart":
            case "drop":
              U = Oc;
              break;
            case "touchcancel":
            case "touchend":
            case "touchmove":
            case "touchstart":
              U = Jc;
              break;
            case Ku:
            case Yu:
            case Xu:
              U = Uc;
              break;
            case Gu:
              U = bc;
              break;
            case "scroll":
              U = Ic;
              break;
            case "wheel":
              U = td;
              break;
            case "copy":
            case "cut":
            case "paste":
              U = Bc;
              break;
            case "gotpointercapture":
            case "lostpointercapture":
            case "pointercancel":
            case "pointerdown":
            case "pointermove":
            case "pointerout":
            case "pointerover":
            case "pointerup":
              U = Pu;
          }
          var H = (t & 4) !== 0, Ee = !H && e === "scroll", v = H ? j !== null ? j + "Capture" : null : j;
          H = [];
          for (var p = w, g; p !== null; ) {
            g = p;
            var T = g.stateNode;
            if (g.tag === 5 && T !== null && (g = T, v !== null && (T = Qn(p, v), T != null && H.push(cr(p, T, g)))), Ee) break;
            p = p.return;
          }
          0 < H.length && (j = new U(j, V, null, n, z), L.push({ event: j, listeners: H }));
        }
      }
      if ((t & 7) === 0) {
        e: {
          if (j = e === "mouseover" || e === "pointerover", U = e === "mouseout" || e === "pointerout", j && n !== Kl && (V = n.relatedTarget || n.fromElement) && (en(V) || V[jt])) break e;
          if ((U || j) && (j = z.window === z ? z : (j = z.ownerDocument) ? j.defaultView || j.parentWindow : window, U ? (V = n.relatedTarget || n.toElement, U = w, V = V ? en(V) : null, V !== null && (Ee = bt(V), V !== Ee || V.tag !== 5 && V.tag !== 6) && (V = null)) : (U = null, V = w), U !== V)) {
            if (H = Cu, T = "onMouseLeave", v = "onMouseEnter", p = "mouse", (e === "pointerout" || e === "pointerover") && (H = Pu, T = "onPointerLeave", v = "onPointerEnter", p = "pointer"), Ee = U == null ? j : En(U), g = V == null ? j : En(V), j = new H(T, p + "leave", U, n, z), j.target = Ee, j.relatedTarget = g, T = null, en(z) === w && (H = new H(v, p + "enter", V, n, z), H.target = g, H.relatedTarget = Ee, T = H), Ee = T, U && V) t: {
              for (H = U, v = V, p = 0, g = H; g; g = jn(g)) p++;
              for (g = 0, T = v; T; T = jn(T)) g++;
              for (; 0 < p - g; ) H = jn(H), p--;
              for (; 0 < g - p; ) v = jn(v), g--;
              for (; p--; ) {
                if (H === v || v !== null && H === v.alternate) break t;
                H = jn(H), v = jn(v);
              }
              H = null;
            }
            else H = null;
            U !== null && ns(L, j, U, H, !1), V !== null && Ee !== null && ns(L, Ee, V, H, !0);
          }
        }
        e: {
          if (j = w ? En(w) : window, U = j.nodeName && j.nodeName.toLowerCase(), U === "select" || U === "input" && j.type === "file") var Q = sd;
          else if (Du(j)) if (Fu) Q = fd;
          else {
            Q = cd;
            var K = ad;
          }
          else (U = j.nodeName) && U.toLowerCase() === "input" && (j.type === "checkbox" || j.type === "radio") && (Q = dd);
          if (Q && (Q = Q(e, w))) {
            Ou(L, Q, n, z);
            break e;
          }
          K && K(e, j, w), e === "focusout" && (K = j._wrapperState) && K.controlled && j.type === "number" && Vl(j, "number", j.value);
        }
        switch (K = w ? En(w) : window, e) {
          case "focusin":
            (Du(K) || K.contentEditable === "true") && (Sn = K, yi = w, ur = null);
            break;
          case "focusout":
            ur = yi = Sn = null;
            break;
          case "mousedown":
            gi = !0;
            break;
          case "contextmenu":
          case "mouseup":
          case "dragend":
            gi = !1, Qu(L, n, z);
            break;
          case "selectionchange":
            if (md) break;
          case "keydown":
          case "keyup":
            Qu(L, n, z);
        }
        var Y;
        if (pi) e: {
          switch (e) {
            case "compositionstart":
              var Z = "onCompositionStart";
              break e;
            case "compositionend":
              Z = "onCompositionEnd";
              break e;
            case "compositionupdate":
              Z = "onCompositionUpdate";
              break e;
          }
          Z = void 0;
        }
        else kn ? Mu(e, n) && (Z = "onCompositionEnd") : e === "keydown" && n.keyCode === 229 && (Z = "onCompositionStart");
        Z && (Ru && n.locale !== "ko" && (kn || Z !== "onCompositionStart" ? Z === "onCompositionEnd" && kn && (Y = Nu()) : (Ft = z, ui = "value" in Ft ? Ft.value : Ft.textContent, kn = !0)), K = Jr(w, Z), 0 < K.length && (Z = new zu(Z, e, null, n, z), L.push({ event: Z, listeners: K }), Y ? Z.data = Y : (Y = Iu(n), Y !== null && (Z.data = Y)))), (Y = rd ? ld(e, n) : id(e, n)) && (w = Jr(w, "onBeforeInput"), 0 < w.length && (z = new zu("onBeforeInput", "beforeinput", null, n, z), L.push({ event: z, listeners: w }), z.data = Y));
      }
      es(L, t);
    });
  }
  function cr(e, t, n) {
    return { instance: e, listener: t, currentTarget: n };
  }
  function Jr(e, t) {
    for (var n = t + "Capture", r = []; e !== null; ) {
      var l = e, i = l.stateNode;
      l.tag === 5 && i !== null && (l = i, i = Qn(e, n), i != null && r.unshift(cr(e, i, l)), i = Qn(e, t), i != null && r.push(cr(e, i, l))), e = e.return;
    }
    return r;
  }
  function jn(e) {
    if (e === null) return null;
    do
      e = e.return;
    while (e && e.tag !== 5);
    return e || null;
  }
  function ns(e, t, n, r, l) {
    for (var i = t._reactName, o = []; n !== null && n !== r; ) {
      var a = n, d = a.alternate, w = a.stateNode;
      if (d !== null && d === r) break;
      a.tag === 5 && w !== null && (a = w, l ? (d = Qn(n, i), d != null && o.unshift(cr(n, d, a))) : l || (d = Qn(n, i), d != null && o.push(cr(n, d, a)))), n = n.return;
    }
    o.length !== 0 && e.push({ event: t, listeners: o });
  }
  var wd = /\r\n?/g, xd = /\u0000|\uFFFD/g;
  function rs(e) {
    return (typeof e == "string" ? e : "" + e).replace(wd, `
`).replace(xd, "");
  }
  function Zr(e, t, n) {
    if (t = rs(t), rs(e) !== t && n) throw Error(s(425));
  }
  function br() {
  }
  var ji = null, Ni = null;
  function Ei(e, t) {
    return e === "textarea" || e === "noscript" || typeof t.children == "string" || typeof t.children == "number" || typeof t.dangerouslySetInnerHTML == "object" && t.dangerouslySetInnerHTML !== null && t.dangerouslySetInnerHTML.__html != null;
  }
  var Ci = typeof setTimeout == "function" ? setTimeout : void 0, kd = typeof clearTimeout == "function" ? clearTimeout : void 0, ls = typeof Promise == "function" ? Promise : void 0, Sd = typeof queueMicrotask == "function" ? queueMicrotask : typeof ls < "u" ? function(e) {
    return ls.resolve(null).then(e).catch(_d);
  } : Ci;
  function _d(e) {
    setTimeout(function() {
      throw e;
    });
  }
  function zi(e, t) {
    var n = t, r = 0;
    do {
      var l = n.nextSibling;
      if (e.removeChild(n), l && l.nodeType === 8) if (n = l.data, n === "/$") {
        if (r === 0) {
          e.removeChild(l), er(t);
          return;
        }
        r--;
      } else n !== "$" && n !== "$?" && n !== "$!" || r++;
      n = l;
    } while (n);
    er(t);
  }
  function Ut(e) {
    for (; e != null; e = e.nextSibling) {
      var t = e.nodeType;
      if (t === 1 || t === 3) break;
      if (t === 8) {
        if (t = e.data, t === "$" || t === "$!" || t === "$?") break;
        if (t === "/$") return null;
      }
    }
    return e;
  }
  function is(e) {
    e = e.previousSibling;
    for (var t = 0; e; ) {
      if (e.nodeType === 8) {
        var n = e.data;
        if (n === "$" || n === "$!" || n === "$?") {
          if (t === 0) return e;
          t--;
        } else n === "/$" && t++;
      }
      e = e.previousSibling;
    }
    return null;
  }
  var Nn = Math.random().toString(36).slice(2), xt = "__reactFiber$" + Nn, dr = "__reactProps$" + Nn, jt = "__reactContainer$" + Nn, Pi = "__reactEvents$" + Nn, jd = "__reactListeners$" + Nn, Nd = "__reactHandles$" + Nn;
  function en(e) {
    var t = e[xt];
    if (t) return t;
    for (var n = e.parentNode; n; ) {
      if (t = n[jt] || n[xt]) {
        if (n = t.alternate, t.child !== null || n !== null && n.child !== null) for (e = is(e); e !== null; ) {
          if (n = e[xt]) return n;
          e = is(e);
        }
        return t;
      }
      e = n, n = e.parentNode;
    }
    return null;
  }
  function fr(e) {
    return e = e[xt] || e[jt], !e || e.tag !== 5 && e.tag !== 6 && e.tag !== 13 && e.tag !== 3 ? null : e;
  }
  function En(e) {
    if (e.tag === 5 || e.tag === 6) return e.stateNode;
    throw Error(s(33));
  }
  function el(e) {
    return e[dr] || null;
  }
  var Ri = [], Cn = -1;
  function At(e) {
    return { current: e };
  }
  function ye(e) {
    0 > Cn || (e.current = Ri[Cn], Ri[Cn] = null, Cn--);
  }
  function me(e, t) {
    Cn++, Ri[Cn] = e.current, e.current = t;
  }
  var Bt = {}, $e = At(Bt), qe = At(!1), tn = Bt;
  function zn(e, t) {
    var n = e.type.contextTypes;
    if (!n) return Bt;
    var r = e.stateNode;
    if (r && r.__reactInternalMemoizedUnmaskedChildContext === t) return r.__reactInternalMemoizedMaskedChildContext;
    var l = {}, i;
    for (i in n) l[i] = t[i];
    return r && (e = e.stateNode, e.__reactInternalMemoizedUnmaskedChildContext = t, e.__reactInternalMemoizedMaskedChildContext = l), l;
  }
  function Ke(e) {
    return e = e.childContextTypes, e != null;
  }
  function tl() {
    ye(qe), ye($e);
  }
  function os(e, t, n) {
    if ($e.current !== Bt) throw Error(s(168));
    me($e, t), me(qe, n);
  }
  function us(e, t, n) {
    var r = e.stateNode;
    if (t = t.childContextTypes, typeof r.getChildContext != "function") return n;
    r = r.getChildContext();
    for (var l in r) if (!(l in t)) throw Error(s(108, he(e) || "Unknown", l));
    return B({}, n, r);
  }
  function nl(e) {
    return e = (e = e.stateNode) && e.__reactInternalMemoizedMergedChildContext || Bt, tn = $e.current, me($e, e), me(qe, qe.current), !0;
  }
  function ss(e, t, n) {
    var r = e.stateNode;
    if (!r) throw Error(s(169));
    n ? (e = us(e, t, tn), r.__reactInternalMemoizedMergedChildContext = e, ye(qe), ye($e), me($e, e)) : ye(qe), me(qe, n);
  }
  var Nt = null, rl = !1, Li = !1;
  function as(e) {
    Nt === null ? Nt = [e] : Nt.push(e);
  }
  function Ed(e) {
    rl = !0, as(e);
  }
  function Vt() {
    if (!Li && Nt !== null) {
      Li = !0;
      var e = 0, t = de;
      try {
        var n = Nt;
        for (de = 1; e < n.length; e++) {
          var r = n[e];
          do
            r = r(!0);
          while (r !== null);
        }
        Nt = null, rl = !1;
      } catch (l) {
        throw Nt !== null && (Nt = Nt.slice(e + 1)), du(bl, Vt), l;
      } finally {
        de = t, Li = !1;
      }
    }
    return null;
  }
  var Pn = [], Rn = 0, ll = null, il = 0, lt = [], it = 0, nn = null, Et = 1, Ct = "";
  function rn(e, t) {
    Pn[Rn++] = il, Pn[Rn++] = ll, ll = e, il = t;
  }
  function cs(e, t, n) {
    lt[it++] = Et, lt[it++] = Ct, lt[it++] = nn, nn = e;
    var r = Et;
    e = Ct;
    var l = 32 - dt(r) - 1;
    r &= ~(1 << l), n += 1;
    var i = 32 - dt(t) + l;
    if (30 < i) {
      var o = l - l % 5;
      i = (r & (1 << o) - 1).toString(32), r >>= o, l -= o, Et = 1 << 32 - dt(t) + l | n << l | r, Ct = i + e;
    } else Et = 1 << i | n << l | r, Ct = e;
  }
  function Ti(e) {
    e.return !== null && (rn(e, 1), cs(e, 1, 0));
  }
  function Mi(e) {
    for (; e === ll; ) ll = Pn[--Rn], Pn[Rn] = null, il = Pn[--Rn], Pn[Rn] = null;
    for (; e === nn; ) nn = lt[--it], lt[it] = null, Ct = lt[--it], lt[it] = null, Et = lt[--it], lt[it] = null;
  }
  var et = null, tt = null, xe = !1, pt = null;
  function ds(e, t) {
    var n = at(5, null, null, 0);
    n.elementType = "DELETED", n.stateNode = t, n.return = e, t = e.deletions, t === null ? (e.deletions = [n], e.flags |= 16) : t.push(n);
  }
  function fs(e, t) {
    switch (e.tag) {
      case 5:
        var n = e.type;
        return t = t.nodeType !== 1 || n.toLowerCase() !== t.nodeName.toLowerCase() ? null : t, t !== null ? (e.stateNode = t, et = e, tt = Ut(t.firstChild), !0) : !1;
      case 6:
        return t = e.pendingProps === "" || t.nodeType !== 3 ? null : t, t !== null ? (e.stateNode = t, et = e, tt = null, !0) : !1;
      case 13:
        return t = t.nodeType !== 8 ? null : t, t !== null ? (n = nn !== null ? { id: Et, overflow: Ct } : null, e.memoizedState = { dehydrated: t, treeContext: n, retryLane: 1073741824 }, n = at(18, null, null, 0), n.stateNode = t, n.return = e, e.child = n, et = e, tt = null, !0) : !1;
      default:
        return !1;
    }
  }
  function Ii(e) {
    return (e.mode & 1) !== 0 && (e.flags & 128) === 0;
  }
  function Di(e) {
    if (xe) {
      var t = tt;
      if (t) {
        var n = t;
        if (!fs(e, t)) {
          if (Ii(e)) throw Error(s(418));
          t = Ut(n.nextSibling);
          var r = et;
          t && fs(e, t) ? ds(r, n) : (e.flags = e.flags & -4097 | 2, xe = !1, et = e);
        }
      } else {
        if (Ii(e)) throw Error(s(418));
        e.flags = e.flags & -4097 | 2, xe = !1, et = e;
      }
    }
  }
  function ps(e) {
    for (e = e.return; e !== null && e.tag !== 5 && e.tag !== 3 && e.tag !== 13; ) e = e.return;
    et = e;
  }
  function ol(e) {
    if (e !== et) return !1;
    if (!xe) return ps(e), xe = !0, !1;
    var t;
    if ((t = e.tag !== 3) && !(t = e.tag !== 5) && (t = e.type, t = t !== "head" && t !== "body" && !Ei(e.type, e.memoizedProps)), t && (t = tt)) {
      if (Ii(e)) throw hs(), Error(s(418));
      for (; t; ) ds(e, t), t = Ut(t.nextSibling);
    }
    if (ps(e), e.tag === 13) {
      if (e = e.memoizedState, e = e !== null ? e.dehydrated : null, !e) throw Error(s(317));
      e: {
        for (e = e.nextSibling, t = 0; e; ) {
          if (e.nodeType === 8) {
            var n = e.data;
            if (n === "/$") {
              if (t === 0) {
                tt = Ut(e.nextSibling);
                break e;
              }
              t--;
            } else n !== "$" && n !== "$!" && n !== "$?" || t++;
          }
          e = e.nextSibling;
        }
        tt = null;
      }
    } else tt = et ? Ut(e.stateNode.nextSibling) : null;
    return !0;
  }
  function hs() {
    for (var e = tt; e; ) e = Ut(e.nextSibling);
  }
  function Ln() {
    tt = et = null, xe = !1;
  }
  function Oi(e) {
    pt === null ? pt = [e] : pt.push(e);
  }
  var Cd = ee.ReactCurrentBatchConfig;
  function pr(e, t, n) {
    if (e = n.ref, e !== null && typeof e != "function" && typeof e != "object") {
      if (n._owner) {
        if (n = n._owner, n) {
          if (n.tag !== 1) throw Error(s(309));
          var r = n.stateNode;
        }
        if (!r) throw Error(s(147, e));
        var l = r, i = "" + e;
        return t !== null && t.ref !== null && typeof t.ref == "function" && t.ref._stringRef === i ? t.ref : (t = function(o) {
          var a = l.refs;
          o === null ? delete a[i] : a[i] = o;
        }, t._stringRef = i, t);
      }
      if (typeof e != "string") throw Error(s(284));
      if (!n._owner) throw Error(s(290, e));
    }
    return e;
  }
  function ul(e, t) {
    throw e = Object.prototype.toString.call(t), Error(s(31, e === "[object Object]" ? "object with keys {" + Object.keys(t).join(", ") + "}" : e));
  }
  function ms(e) {
    var t = e._init;
    return t(e._payload);
  }
  function vs(e) {
    function t(v, p) {
      if (e) {
        var g = v.deletions;
        g === null ? (v.deletions = [p], v.flags |= 16) : g.push(p);
      }
    }
    function n(v, p) {
      if (!e) return null;
      for (; p !== null; ) t(v, p), p = p.sibling;
      return null;
    }
    function r(v, p) {
      for (v = /* @__PURE__ */ new Map(); p !== null; ) p.key !== null ? v.set(p.key, p) : v.set(p.index, p), p = p.sibling;
      return v;
    }
    function l(v, p) {
      return v = Gt(v, p), v.index = 0, v.sibling = null, v;
    }
    function i(v, p, g) {
      return v.index = g, e ? (g = v.alternate, g !== null ? (g = g.index, g < p ? (v.flags |= 2, p) : g) : (v.flags |= 2, p)) : (v.flags |= 1048576, p);
    }
    function o(v) {
      return e && v.alternate === null && (v.flags |= 2), v;
    }
    function a(v, p, g, T) {
      return p === null || p.tag !== 6 ? (p = zo(g, v.mode, T), p.return = v, p) : (p = l(p, g), p.return = v, p);
    }
    function d(v, p, g, T) {
      var Q = g.type;
      return Q === q ? z(v, p, g.props.children, T, g.key) : p !== null && (p.elementType === Q || typeof Q == "object" && Q !== null && Q.$$typeof === Qe && ms(Q) === p.type) ? (T = l(p, g.props), T.ref = pr(v, p, g), T.return = v, T) : (T = Ll(g.type, g.key, g.props, null, v.mode, T), T.ref = pr(v, p, g), T.return = v, T);
    }
    function w(v, p, g, T) {
      return p === null || p.tag !== 4 || p.stateNode.containerInfo !== g.containerInfo || p.stateNode.implementation !== g.implementation ? (p = Po(g, v.mode, T), p.return = v, p) : (p = l(p, g.children || []), p.return = v, p);
    }
    function z(v, p, g, T, Q) {
      return p === null || p.tag !== 7 ? (p = fn(g, v.mode, T, Q), p.return = v, p) : (p = l(p, g), p.return = v, p);
    }
    function L(v, p, g) {
      if (typeof p == "string" && p !== "" || typeof p == "number") return p = zo("" + p, v.mode, g), p.return = v, p;
      if (typeof p == "object" && p !== null) {
        switch (p.$$typeof) {
          case fe:
            return g = Ll(p.type, p.key, p.props, null, v.mode, g), g.ref = pr(v, null, p), g.return = v, g;
          case ge:
            return p = Po(p, v.mode, g), p.return = v, p;
          case Qe:
            var T = p._init;
            return L(v, T(p._payload), g);
        }
        if (Vn(p) || J(p)) return p = fn(p, v.mode, g, null), p.return = v, p;
        ul(v, p);
      }
      return null;
    }
    function j(v, p, g, T) {
      var Q = p !== null ? p.key : null;
      if (typeof g == "string" && g !== "" || typeof g == "number") return Q !== null ? null : a(v, p, "" + g, T);
      if (typeof g == "object" && g !== null) {
        switch (g.$$typeof) {
          case fe:
            return g.key === Q ? d(v, p, g, T) : null;
          case ge:
            return g.key === Q ? w(v, p, g, T) : null;
          case Qe:
            return Q = g._init, j(
              v,
              p,
              Q(g._payload),
              T
            );
        }
        if (Vn(g) || J(g)) return Q !== null ? null : z(v, p, g, T, null);
        ul(v, g);
      }
      return null;
    }
    function U(v, p, g, T, Q) {
      if (typeof T == "string" && T !== "" || typeof T == "number") return v = v.get(g) || null, a(p, v, "" + T, Q);
      if (typeof T == "object" && T !== null) {
        switch (T.$$typeof) {
          case fe:
            return v = v.get(T.key === null ? g : T.key) || null, d(p, v, T, Q);
          case ge:
            return v = v.get(T.key === null ? g : T.key) || null, w(p, v, T, Q);
          case Qe:
            var K = T._init;
            return U(v, p, g, K(T._payload), Q);
        }
        if (Vn(T) || J(T)) return v = v.get(g) || null, z(p, v, T, Q, null);
        ul(p, T);
      }
      return null;
    }
    function V(v, p, g, T) {
      for (var Q = null, K = null, Y = p, Z = p = 0, Me = null; Y !== null && Z < g.length; Z++) {
        Y.index > Z ? (Me = Y, Y = null) : Me = Y.sibling;
        var se = j(v, Y, g[Z], T);
        if (se === null) {
          Y === null && (Y = Me);
          break;
        }
        e && Y && se.alternate === null && t(v, Y), p = i(se, p, Z), K === null ? Q = se : K.sibling = se, K = se, Y = Me;
      }
      if (Z === g.length) return n(v, Y), xe && rn(v, Z), Q;
      if (Y === null) {
        for (; Z < g.length; Z++) Y = L(v, g[Z], T), Y !== null && (p = i(Y, p, Z), K === null ? Q = Y : K.sibling = Y, K = Y);
        return xe && rn(v, Z), Q;
      }
      for (Y = r(v, Y); Z < g.length; Z++) Me = U(Y, v, Z, g[Z], T), Me !== null && (e && Me.alternate !== null && Y.delete(Me.key === null ? Z : Me.key), p = i(Me, p, Z), K === null ? Q = Me : K.sibling = Me, K = Me);
      return e && Y.forEach(function(Jt) {
        return t(v, Jt);
      }), xe && rn(v, Z), Q;
    }
    function H(v, p, g, T) {
      var Q = J(g);
      if (typeof Q != "function") throw Error(s(150));
      if (g = Q.call(g), g == null) throw Error(s(151));
      for (var K = Q = null, Y = p, Z = p = 0, Me = null, se = g.next(); Y !== null && !se.done; Z++, se = g.next()) {
        Y.index > Z ? (Me = Y, Y = null) : Me = Y.sibling;
        var Jt = j(v, Y, se.value, T);
        if (Jt === null) {
          Y === null && (Y = Me);
          break;
        }
        e && Y && Jt.alternate === null && t(v, Y), p = i(Jt, p, Z), K === null ? Q = Jt : K.sibling = Jt, K = Jt, Y = Me;
      }
      if (se.done) return n(
        v,
        Y
      ), xe && rn(v, Z), Q;
      if (Y === null) {
        for (; !se.done; Z++, se = g.next()) se = L(v, se.value, T), se !== null && (p = i(se, p, Z), K === null ? Q = se : K.sibling = se, K = se);
        return xe && rn(v, Z), Q;
      }
      for (Y = r(v, Y); !se.done; Z++, se = g.next()) se = U(Y, v, Z, se.value, T), se !== null && (e && se.alternate !== null && Y.delete(se.key === null ? Z : se.key), p = i(se, p, Z), K === null ? Q = se : K.sibling = se, K = se);
      return e && Y.forEach(function(uf) {
        return t(v, uf);
      }), xe && rn(v, Z), Q;
    }
    function Ee(v, p, g, T) {
      if (typeof g == "object" && g !== null && g.type === q && g.key === null && (g = g.props.children), typeof g == "object" && g !== null) {
        switch (g.$$typeof) {
          case fe:
            e: {
              for (var Q = g.key, K = p; K !== null; ) {
                if (K.key === Q) {
                  if (Q = g.type, Q === q) {
                    if (K.tag === 7) {
                      n(v, K.sibling), p = l(K, g.props.children), p.return = v, v = p;
                      break e;
                    }
                  } else if (K.elementType === Q || typeof Q == "object" && Q !== null && Q.$$typeof === Qe && ms(Q) === K.type) {
                    n(v, K.sibling), p = l(K, g.props), p.ref = pr(v, K, g), p.return = v, v = p;
                    break e;
                  }
                  n(v, K);
                  break;
                } else t(v, K);
                K = K.sibling;
              }
              g.type === q ? (p = fn(g.props.children, v.mode, T, g.key), p.return = v, v = p) : (T = Ll(g.type, g.key, g.props, null, v.mode, T), T.ref = pr(v, p, g), T.return = v, v = T);
            }
            return o(v);
          case ge:
            e: {
              for (K = g.key; p !== null; ) {
                if (p.key === K) if (p.tag === 4 && p.stateNode.containerInfo === g.containerInfo && p.stateNode.implementation === g.implementation) {
                  n(v, p.sibling), p = l(p, g.children || []), p.return = v, v = p;
                  break e;
                } else {
                  n(v, p);
                  break;
                }
                else t(v, p);
                p = p.sibling;
              }
              p = Po(g, v.mode, T), p.return = v, v = p;
            }
            return o(v);
          case Qe:
            return K = g._init, Ee(v, p, K(g._payload), T);
        }
        if (Vn(g)) return V(v, p, g, T);
        if (J(g)) return H(v, p, g, T);
        ul(v, g);
      }
      return typeof g == "string" && g !== "" || typeof g == "number" ? (g = "" + g, p !== null && p.tag === 6 ? (n(v, p.sibling), p = l(p, g), p.return = v, v = p) : (n(v, p), p = zo(g, v.mode, T), p.return = v, v = p), o(v)) : n(v, p);
    }
    return Ee;
  }
  var Tn = vs(!0), ys = vs(!1), sl = At(null), al = null, Mn = null, Fi = null;
  function $i() {
    Fi = Mn = al = null;
  }
  function Ui(e) {
    var t = sl.current;
    ye(sl), e._currentValue = t;
  }
  function Ai(e, t, n) {
    for (; e !== null; ) {
      var r = e.alternate;
      if ((e.childLanes & t) !== t ? (e.childLanes |= t, r !== null && (r.childLanes |= t)) : r !== null && (r.childLanes & t) !== t && (r.childLanes |= t), e === n) break;
      e = e.return;
    }
  }
  function In(e, t) {
    al = e, Fi = Mn = null, e = e.dependencies, e !== null && e.firstContext !== null && ((e.lanes & t) !== 0 && (Ye = !0), e.firstContext = null);
  }
  function ot(e) {
    var t = e._currentValue;
    if (Fi !== e) if (e = { context: e, memoizedValue: t, next: null }, Mn === null) {
      if (al === null) throw Error(s(308));
      Mn = e, al.dependencies = { lanes: 0, firstContext: e };
    } else Mn = Mn.next = e;
    return t;
  }
  var ln = null;
  function Bi(e) {
    ln === null ? ln = [e] : ln.push(e);
  }
  function gs(e, t, n, r) {
    var l = t.interleaved;
    return l === null ? (n.next = n, Bi(t)) : (n.next = l.next, l.next = n), t.interleaved = n, zt(e, r);
  }
  function zt(e, t) {
    e.lanes |= t;
    var n = e.alternate;
    for (n !== null && (n.lanes |= t), n = e, e = e.return; e !== null; ) e.childLanes |= t, n = e.alternate, n !== null && (n.childLanes |= t), n = e, e = e.return;
    return n.tag === 3 ? n.stateNode : null;
  }
  var Wt = !1;
  function Vi(e) {
    e.updateQueue = { baseState: e.memoizedState, firstBaseUpdate: null, lastBaseUpdate: null, shared: { pending: null, interleaved: null, lanes: 0 }, effects: null };
  }
  function ws(e, t) {
    e = e.updateQueue, t.updateQueue === e && (t.updateQueue = { baseState: e.baseState, firstBaseUpdate: e.firstBaseUpdate, lastBaseUpdate: e.lastBaseUpdate, shared: e.shared, effects: e.effects });
  }
  function Pt(e, t) {
    return { eventTime: e, lane: t, tag: 0, payload: null, callback: null, next: null };
  }
  function Ht(e, t, n) {
    var r = e.updateQueue;
    if (r === null) return null;
    if (r = r.shared, (ie & 2) !== 0) {
      var l = r.pending;
      return l === null ? t.next = t : (t.next = l.next, l.next = t), r.pending = t, zt(e, n);
    }
    return l = r.interleaved, l === null ? (t.next = t, Bi(r)) : (t.next = l.next, l.next = t), r.interleaved = t, zt(e, n);
  }
  function cl(e, t, n) {
    if (t = t.updateQueue, t !== null && (t = t.shared, (n & 4194240) !== 0)) {
      var r = t.lanes;
      r &= e.pendingLanes, n |= r, t.lanes = n, ni(e, n);
    }
  }
  function xs(e, t) {
    var n = e.updateQueue, r = e.alternate;
    if (r !== null && (r = r.updateQueue, n === r)) {
      var l = null, i = null;
      if (n = n.firstBaseUpdate, n !== null) {
        do {
          var o = { eventTime: n.eventTime, lane: n.lane, tag: n.tag, payload: n.payload, callback: n.callback, next: null };
          i === null ? l = i = o : i = i.next = o, n = n.next;
        } while (n !== null);
        i === null ? l = i = t : i = i.next = t;
      } else l = i = t;
      n = { baseState: r.baseState, firstBaseUpdate: l, lastBaseUpdate: i, shared: r.shared, effects: r.effects }, e.updateQueue = n;
      return;
    }
    e = n.lastBaseUpdate, e === null ? n.firstBaseUpdate = t : e.next = t, n.lastBaseUpdate = t;
  }
  function dl(e, t, n, r) {
    var l = e.updateQueue;
    Wt = !1;
    var i = l.firstBaseUpdate, o = l.lastBaseUpdate, a = l.shared.pending;
    if (a !== null) {
      l.shared.pending = null;
      var d = a, w = d.next;
      d.next = null, o === null ? i = w : o.next = w, o = d;
      var z = e.alternate;
      z !== null && (z = z.updateQueue, a = z.lastBaseUpdate, a !== o && (a === null ? z.firstBaseUpdate = w : a.next = w, z.lastBaseUpdate = d));
    }
    if (i !== null) {
      var L = l.baseState;
      o = 0, z = w = d = null, a = i;
      do {
        var j = a.lane, U = a.eventTime;
        if ((r & j) === j) {
          z !== null && (z = z.next = {
            eventTime: U,
            lane: 0,
            tag: a.tag,
            payload: a.payload,
            callback: a.callback,
            next: null
          });
          e: {
            var V = e, H = a;
            switch (j = t, U = n, H.tag) {
              case 1:
                if (V = H.payload, typeof V == "function") {
                  L = V.call(U, L, j);
                  break e;
                }
                L = V;
                break e;
              case 3:
                V.flags = V.flags & -65537 | 128;
              case 0:
                if (V = H.payload, j = typeof V == "function" ? V.call(U, L, j) : V, j == null) break e;
                L = B({}, L, j);
                break e;
              case 2:
                Wt = !0;
            }
          }
          a.callback !== null && a.lane !== 0 && (e.flags |= 64, j = l.effects, j === null ? l.effects = [a] : j.push(a));
        } else U = { eventTime: U, lane: j, tag: a.tag, payload: a.payload, callback: a.callback, next: null }, z === null ? (w = z = U, d = L) : z = z.next = U, o |= j;
        if (a = a.next, a === null) {
          if (a = l.shared.pending, a === null) break;
          j = a, a = j.next, j.next = null, l.lastBaseUpdate = j, l.shared.pending = null;
        }
      } while (!0);
      if (z === null && (d = L), l.baseState = d, l.firstBaseUpdate = w, l.lastBaseUpdate = z, t = l.shared.interleaved, t !== null) {
        l = t;
        do
          o |= l.lane, l = l.next;
        while (l !== t);
      } else i === null && (l.shared.lanes = 0);
      sn |= o, e.lanes = o, e.memoizedState = L;
    }
  }
  function ks(e, t, n) {
    if (e = t.effects, t.effects = null, e !== null) for (t = 0; t < e.length; t++) {
      var r = e[t], l = r.callback;
      if (l !== null) {
        if (r.callback = null, r = n, typeof l != "function") throw Error(s(191, l));
        l.call(r);
      }
    }
  }
  var hr = {}, kt = At(hr), mr = At(hr), vr = At(hr);
  function on(e) {
    if (e === hr) throw Error(s(174));
    return e;
  }
  function Wi(e, t) {
    switch (me(vr, t), me(mr, e), me(kt, hr), e = t.nodeType, e) {
      case 9:
      case 11:
        t = (t = t.documentElement) ? t.namespaceURI : Hl(null, "");
        break;
      default:
        e = e === 8 ? t.parentNode : t, t = e.namespaceURI || null, e = e.tagName, t = Hl(t, e);
    }
    ye(kt), me(kt, t);
  }
  function Dn() {
    ye(kt), ye(mr), ye(vr);
  }
  function Ss(e) {
    on(vr.current);
    var t = on(kt.current), n = Hl(t, e.type);
    t !== n && (me(mr, e), me(kt, n));
  }
  function Hi(e) {
    mr.current === e && (ye(kt), ye(mr));
  }
  var ke = At(0);
  function fl(e) {
    for (var t = e; t !== null; ) {
      if (t.tag === 13) {
        var n = t.memoizedState;
        if (n !== null && (n = n.dehydrated, n === null || n.data === "$?" || n.data === "$!")) return t;
      } else if (t.tag === 19 && t.memoizedProps.revealOrder !== void 0) {
        if ((t.flags & 128) !== 0) return t;
      } else if (t.child !== null) {
        t.child.return = t, t = t.child;
        continue;
      }
      if (t === e) break;
      for (; t.sibling === null; ) {
        if (t.return === null || t.return === e) return null;
        t = t.return;
      }
      t.sibling.return = t.return, t = t.sibling;
    }
    return null;
  }
  var Qi = [];
  function qi() {
    for (var e = 0; e < Qi.length; e++) Qi[e]._workInProgressVersionPrimary = null;
    Qi.length = 0;
  }
  var pl = ee.ReactCurrentDispatcher, Ki = ee.ReactCurrentBatchConfig, un = 0, Se = null, Pe = null, Le = null, hl = !1, yr = !1, gr = 0, zd = 0;
  function Ue() {
    throw Error(s(321));
  }
  function Yi(e, t) {
    if (t === null) return !1;
    for (var n = 0; n < t.length && n < e.length; n++) if (!ft(e[n], t[n])) return !1;
    return !0;
  }
  function Xi(e, t, n, r, l, i) {
    if (un = i, Se = t, t.memoizedState = null, t.updateQueue = null, t.lanes = 0, pl.current = e === null || e.memoizedState === null ? Td : Md, e = n(r, l), yr) {
      i = 0;
      do {
        if (yr = !1, gr = 0, 25 <= i) throw Error(s(301));
        i += 1, Le = Pe = null, t.updateQueue = null, pl.current = Id, e = n(r, l);
      } while (yr);
    }
    if (pl.current = yl, t = Pe !== null && Pe.next !== null, un = 0, Le = Pe = Se = null, hl = !1, t) throw Error(s(300));
    return e;
  }
  function Gi() {
    var e = gr !== 0;
    return gr = 0, e;
  }
  function St() {
    var e = { memoizedState: null, baseState: null, baseQueue: null, queue: null, next: null };
    return Le === null ? Se.memoizedState = Le = e : Le = Le.next = e, Le;
  }
  function ut() {
    if (Pe === null) {
      var e = Se.alternate;
      e = e !== null ? e.memoizedState : null;
    } else e = Pe.next;
    var t = Le === null ? Se.memoizedState : Le.next;
    if (t !== null) Le = t, Pe = e;
    else {
      if (e === null) throw Error(s(310));
      Pe = e, e = { memoizedState: Pe.memoizedState, baseState: Pe.baseState, baseQueue: Pe.baseQueue, queue: Pe.queue, next: null }, Le === null ? Se.memoizedState = Le = e : Le = Le.next = e;
    }
    return Le;
  }
  function wr(e, t) {
    return typeof t == "function" ? t(e) : t;
  }
  function Ji(e) {
    var t = ut(), n = t.queue;
    if (n === null) throw Error(s(311));
    n.lastRenderedReducer = e;
    var r = Pe, l = r.baseQueue, i = n.pending;
    if (i !== null) {
      if (l !== null) {
        var o = l.next;
        l.next = i.next, i.next = o;
      }
      r.baseQueue = l = i, n.pending = null;
    }
    if (l !== null) {
      i = l.next, r = r.baseState;
      var a = o = null, d = null, w = i;
      do {
        var z = w.lane;
        if ((un & z) === z) d !== null && (d = d.next = { lane: 0, action: w.action, hasEagerState: w.hasEagerState, eagerState: w.eagerState, next: null }), r = w.hasEagerState ? w.eagerState : e(r, w.action);
        else {
          var L = {
            lane: z,
            action: w.action,
            hasEagerState: w.hasEagerState,
            eagerState: w.eagerState,
            next: null
          };
          d === null ? (a = d = L, o = r) : d = d.next = L, Se.lanes |= z, sn |= z;
        }
        w = w.next;
      } while (w !== null && w !== i);
      d === null ? o = r : d.next = a, ft(r, t.memoizedState) || (Ye = !0), t.memoizedState = r, t.baseState = o, t.baseQueue = d, n.lastRenderedState = r;
    }
    if (e = n.interleaved, e !== null) {
      l = e;
      do
        i = l.lane, Se.lanes |= i, sn |= i, l = l.next;
      while (l !== e);
    } else l === null && (n.lanes = 0);
    return [t.memoizedState, n.dispatch];
  }
  function Zi(e) {
    var t = ut(), n = t.queue;
    if (n === null) throw Error(s(311));
    n.lastRenderedReducer = e;
    var r = n.dispatch, l = n.pending, i = t.memoizedState;
    if (l !== null) {
      n.pending = null;
      var o = l = l.next;
      do
        i = e(i, o.action), o = o.next;
      while (o !== l);
      ft(i, t.memoizedState) || (Ye = !0), t.memoizedState = i, t.baseQueue === null && (t.baseState = i), n.lastRenderedState = i;
    }
    return [i, r];
  }
  function _s() {
  }
  function js(e, t) {
    var n = Se, r = ut(), l = t(), i = !ft(r.memoizedState, l);
    if (i && (r.memoizedState = l, Ye = !0), r = r.queue, bi(Cs.bind(null, n, r, e), [e]), r.getSnapshot !== t || i || Le !== null && Le.memoizedState.tag & 1) {
      if (n.flags |= 2048, xr(9, Es.bind(null, n, r, l, t), void 0, null), Te === null) throw Error(s(349));
      (un & 30) !== 0 || Ns(n, t, l);
    }
    return l;
  }
  function Ns(e, t, n) {
    e.flags |= 16384, e = { getSnapshot: t, value: n }, t = Se.updateQueue, t === null ? (t = { lastEffect: null, stores: null }, Se.updateQueue = t, t.stores = [e]) : (n = t.stores, n === null ? t.stores = [e] : n.push(e));
  }
  function Es(e, t, n, r) {
    t.value = n, t.getSnapshot = r, zs(t) && Ps(e);
  }
  function Cs(e, t, n) {
    return n(function() {
      zs(t) && Ps(e);
    });
  }
  function zs(e) {
    var t = e.getSnapshot;
    e = e.value;
    try {
      var n = t();
      return !ft(e, n);
    } catch {
      return !0;
    }
  }
  function Ps(e) {
    var t = zt(e, 1);
    t !== null && yt(t, e, 1, -1);
  }
  function Rs(e) {
    var t = St();
    return typeof e == "function" && (e = e()), t.memoizedState = t.baseState = e, e = { pending: null, interleaved: null, lanes: 0, dispatch: null, lastRenderedReducer: wr, lastRenderedState: e }, t.queue = e, e = e.dispatch = Ld.bind(null, Se, e), [t.memoizedState, e];
  }
  function xr(e, t, n, r) {
    return e = { tag: e, create: t, destroy: n, deps: r, next: null }, t = Se.updateQueue, t === null ? (t = { lastEffect: null, stores: null }, Se.updateQueue = t, t.lastEffect = e.next = e) : (n = t.lastEffect, n === null ? t.lastEffect = e.next = e : (r = n.next, n.next = e, e.next = r, t.lastEffect = e)), e;
  }
  function Ls() {
    return ut().memoizedState;
  }
  function ml(e, t, n, r) {
    var l = St();
    Se.flags |= e, l.memoizedState = xr(1 | t, n, void 0, r === void 0 ? null : r);
  }
  function vl(e, t, n, r) {
    var l = ut();
    r = r === void 0 ? null : r;
    var i = void 0;
    if (Pe !== null) {
      var o = Pe.memoizedState;
      if (i = o.destroy, r !== null && Yi(r, o.deps)) {
        l.memoizedState = xr(t, n, i, r);
        return;
      }
    }
    Se.flags |= e, l.memoizedState = xr(1 | t, n, i, r);
  }
  function Ts(e, t) {
    return ml(8390656, 8, e, t);
  }
  function bi(e, t) {
    return vl(2048, 8, e, t);
  }
  function Ms(e, t) {
    return vl(4, 2, e, t);
  }
  function Is(e, t) {
    return vl(4, 4, e, t);
  }
  function Ds(e, t) {
    if (typeof t == "function") return e = e(), t(e), function() {
      t(null);
    };
    if (t != null) return e = e(), t.current = e, function() {
      t.current = null;
    };
  }
  function Os(e, t, n) {
    return n = n != null ? n.concat([e]) : null, vl(4, 4, Ds.bind(null, t, e), n);
  }
  function eo() {
  }
  function Fs(e, t) {
    var n = ut();
    t = t === void 0 ? null : t;
    var r = n.memoizedState;
    return r !== null && t !== null && Yi(t, r[1]) ? r[0] : (n.memoizedState = [e, t], e);
  }
  function $s(e, t) {
    var n = ut();
    t = t === void 0 ? null : t;
    var r = n.memoizedState;
    return r !== null && t !== null && Yi(t, r[1]) ? r[0] : (e = e(), n.memoizedState = [e, t], e);
  }
  function Us(e, t, n) {
    return (un & 21) === 0 ? (e.baseState && (e.baseState = !1, Ye = !0), e.memoizedState = n) : (ft(n, t) || (n = mu(), Se.lanes |= n, sn |= n, e.baseState = !0), t);
  }
  function Pd(e, t) {
    var n = de;
    de = n !== 0 && 4 > n ? n : 4, e(!0);
    var r = Ki.transition;
    Ki.transition = {};
    try {
      e(!1), t();
    } finally {
      de = n, Ki.transition = r;
    }
  }
  function As() {
    return ut().memoizedState;
  }
  function Rd(e, t, n) {
    var r = Yt(e);
    if (n = { lane: r, action: n, hasEagerState: !1, eagerState: null, next: null }, Bs(e)) Vs(t, n);
    else if (n = gs(e, t, n, r), n !== null) {
      var l = He();
      yt(n, e, r, l), Ws(n, t, r);
    }
  }
  function Ld(e, t, n) {
    var r = Yt(e), l = { lane: r, action: n, hasEagerState: !1, eagerState: null, next: null };
    if (Bs(e)) Vs(t, l);
    else {
      var i = e.alternate;
      if (e.lanes === 0 && (i === null || i.lanes === 0) && (i = t.lastRenderedReducer, i !== null)) try {
        var o = t.lastRenderedState, a = i(o, n);
        if (l.hasEagerState = !0, l.eagerState = a, ft(a, o)) {
          var d = t.interleaved;
          d === null ? (l.next = l, Bi(t)) : (l.next = d.next, d.next = l), t.interleaved = l;
          return;
        }
      } catch {
      } finally {
      }
      n = gs(e, t, l, r), n !== null && (l = He(), yt(n, e, r, l), Ws(n, t, r));
    }
  }
  function Bs(e) {
    var t = e.alternate;
    return e === Se || t !== null && t === Se;
  }
  function Vs(e, t) {
    yr = hl = !0;
    var n = e.pending;
    n === null ? t.next = t : (t.next = n.next, n.next = t), e.pending = t;
  }
  function Ws(e, t, n) {
    if ((n & 4194240) !== 0) {
      var r = t.lanes;
      r &= e.pendingLanes, n |= r, t.lanes = n, ni(e, n);
    }
  }
  var yl = { readContext: ot, useCallback: Ue, useContext: Ue, useEffect: Ue, useImperativeHandle: Ue, useInsertionEffect: Ue, useLayoutEffect: Ue, useMemo: Ue, useReducer: Ue, useRef: Ue, useState: Ue, useDebugValue: Ue, useDeferredValue: Ue, useTransition: Ue, useMutableSource: Ue, useSyncExternalStore: Ue, useId: Ue, unstable_isNewReconciler: !1 }, Td = { readContext: ot, useCallback: function(e, t) {
    return St().memoizedState = [e, t === void 0 ? null : t], e;
  }, useContext: ot, useEffect: Ts, useImperativeHandle: function(e, t, n) {
    return n = n != null ? n.concat([e]) : null, ml(
      4194308,
      4,
      Ds.bind(null, t, e),
      n
    );
  }, useLayoutEffect: function(e, t) {
    return ml(4194308, 4, e, t);
  }, useInsertionEffect: function(e, t) {
    return ml(4, 2, e, t);
  }, useMemo: function(e, t) {
    var n = St();
    return t = t === void 0 ? null : t, e = e(), n.memoizedState = [e, t], e;
  }, useReducer: function(e, t, n) {
    var r = St();
    return t = n !== void 0 ? n(t) : t, r.memoizedState = r.baseState = t, e = { pending: null, interleaved: null, lanes: 0, dispatch: null, lastRenderedReducer: e, lastRenderedState: t }, r.queue = e, e = e.dispatch = Rd.bind(null, Se, e), [r.memoizedState, e];
  }, useRef: function(e) {
    var t = St();
    return e = { current: e }, t.memoizedState = e;
  }, useState: Rs, useDebugValue: eo, useDeferredValue: function(e) {
    return St().memoizedState = e;
  }, useTransition: function() {
    var e = Rs(!1), t = e[0];
    return e = Pd.bind(null, e[1]), St().memoizedState = e, [t, e];
  }, useMutableSource: function() {
  }, useSyncExternalStore: function(e, t, n) {
    var r = Se, l = St();
    if (xe) {
      if (n === void 0) throw Error(s(407));
      n = n();
    } else {
      if (n = t(), Te === null) throw Error(s(349));
      (un & 30) !== 0 || Ns(r, t, n);
    }
    l.memoizedState = n;
    var i = { value: n, getSnapshot: t };
    return l.queue = i, Ts(Cs.bind(
      null,
      r,
      i,
      e
    ), [e]), r.flags |= 2048, xr(9, Es.bind(null, r, i, n, t), void 0, null), n;
  }, useId: function() {
    var e = St(), t = Te.identifierPrefix;
    if (xe) {
      var n = Ct, r = Et;
      n = (r & ~(1 << 32 - dt(r) - 1)).toString(32) + n, t = ":" + t + "R" + n, n = gr++, 0 < n && (t += "H" + n.toString(32)), t += ":";
    } else n = zd++, t = ":" + t + "r" + n.toString(32) + ":";
    return e.memoizedState = t;
  }, unstable_isNewReconciler: !1 }, Md = {
    readContext: ot,
    useCallback: Fs,
    useContext: ot,
    useEffect: bi,
    useImperativeHandle: Os,
    useInsertionEffect: Ms,
    useLayoutEffect: Is,
    useMemo: $s,
    useReducer: Ji,
    useRef: Ls,
    useState: function() {
      return Ji(wr);
    },
    useDebugValue: eo,
    useDeferredValue: function(e) {
      var t = ut();
      return Us(t, Pe.memoizedState, e);
    },
    useTransition: function() {
      var e = Ji(wr)[0], t = ut().memoizedState;
      return [e, t];
    },
    useMutableSource: _s,
    useSyncExternalStore: js,
    useId: As,
    unstable_isNewReconciler: !1
  }, Id = { readContext: ot, useCallback: Fs, useContext: ot, useEffect: bi, useImperativeHandle: Os, useInsertionEffect: Ms, useLayoutEffect: Is, useMemo: $s, useReducer: Zi, useRef: Ls, useState: function() {
    return Zi(wr);
  }, useDebugValue: eo, useDeferredValue: function(e) {
    var t = ut();
    return Pe === null ? t.memoizedState = e : Us(t, Pe.memoizedState, e);
  }, useTransition: function() {
    var e = Zi(wr)[0], t = ut().memoizedState;
    return [e, t];
  }, useMutableSource: _s, useSyncExternalStore: js, useId: As, unstable_isNewReconciler: !1 };
  function ht(e, t) {
    if (e && e.defaultProps) {
      t = B({}, t), e = e.defaultProps;
      for (var n in e) t[n] === void 0 && (t[n] = e[n]);
      return t;
    }
    return t;
  }
  function to(e, t, n, r) {
    t = e.memoizedState, n = n(r, t), n = n == null ? t : B({}, t, n), e.memoizedState = n, e.lanes === 0 && (e.updateQueue.baseState = n);
  }
  var gl = { isMounted: function(e) {
    return (e = e._reactInternals) ? bt(e) === e : !1;
  }, enqueueSetState: function(e, t, n) {
    e = e._reactInternals;
    var r = He(), l = Yt(e), i = Pt(r, l);
    i.payload = t, n != null && (i.callback = n), t = Ht(e, i, l), t !== null && (yt(t, e, l, r), cl(t, e, l));
  }, enqueueReplaceState: function(e, t, n) {
    e = e._reactInternals;
    var r = He(), l = Yt(e), i = Pt(r, l);
    i.tag = 1, i.payload = t, n != null && (i.callback = n), t = Ht(e, i, l), t !== null && (yt(t, e, l, r), cl(t, e, l));
  }, enqueueForceUpdate: function(e, t) {
    e = e._reactInternals;
    var n = He(), r = Yt(e), l = Pt(n, r);
    l.tag = 2, t != null && (l.callback = t), t = Ht(e, l, r), t !== null && (yt(t, e, r, n), cl(t, e, r));
  } };
  function Hs(e, t, n, r, l, i, o) {
    return e = e.stateNode, typeof e.shouldComponentUpdate == "function" ? e.shouldComponentUpdate(r, i, o) : t.prototype && t.prototype.isPureReactComponent ? !or(n, r) || !or(l, i) : !0;
  }
  function Qs(e, t, n) {
    var r = !1, l = Bt, i = t.contextType;
    return typeof i == "object" && i !== null ? i = ot(i) : (l = Ke(t) ? tn : $e.current, r = t.contextTypes, i = (r = r != null) ? zn(e, l) : Bt), t = new t(n, i), e.memoizedState = t.state !== null && t.state !== void 0 ? t.state : null, t.updater = gl, e.stateNode = t, t._reactInternals = e, r && (e = e.stateNode, e.__reactInternalMemoizedUnmaskedChildContext = l, e.__reactInternalMemoizedMaskedChildContext = i), t;
  }
  function qs(e, t, n, r) {
    e = t.state, typeof t.componentWillReceiveProps == "function" && t.componentWillReceiveProps(n, r), typeof t.UNSAFE_componentWillReceiveProps == "function" && t.UNSAFE_componentWillReceiveProps(n, r), t.state !== e && gl.enqueueReplaceState(t, t.state, null);
  }
  function no(e, t, n, r) {
    var l = e.stateNode;
    l.props = n, l.state = e.memoizedState, l.refs = {}, Vi(e);
    var i = t.contextType;
    typeof i == "object" && i !== null ? l.context = ot(i) : (i = Ke(t) ? tn : $e.current, l.context = zn(e, i)), l.state = e.memoizedState, i = t.getDerivedStateFromProps, typeof i == "function" && (to(e, t, i, n), l.state = e.memoizedState), typeof t.getDerivedStateFromProps == "function" || typeof l.getSnapshotBeforeUpdate == "function" || typeof l.UNSAFE_componentWillMount != "function" && typeof l.componentWillMount != "function" || (t = l.state, typeof l.componentWillMount == "function" && l.componentWillMount(), typeof l.UNSAFE_componentWillMount == "function" && l.UNSAFE_componentWillMount(), t !== l.state && gl.enqueueReplaceState(l, l.state, null), dl(e, n, l, r), l.state = e.memoizedState), typeof l.componentDidMount == "function" && (e.flags |= 4194308);
  }
  function On(e, t) {
    try {
      var n = "", r = t;
      do
        n += oe(r), r = r.return;
      while (r);
      var l = n;
    } catch (i) {
      l = `
Error generating stack: ` + i.message + `
` + i.stack;
    }
    return { value: e, source: t, stack: l, digest: null };
  }
  function ro(e, t, n) {
    return { value: e, source: null, stack: n ?? null, digest: t ?? null };
  }
  function lo(e, t) {
    try {
      console.error(t.value);
    } catch (n) {
      setTimeout(function() {
        throw n;
      });
    }
  }
  var Dd = typeof WeakMap == "function" ? WeakMap : Map;
  function Ks(e, t, n) {
    n = Pt(-1, n), n.tag = 3, n.payload = { element: null };
    var r = t.value;
    return n.callback = function() {
      Nl || (Nl = !0, xo = r), lo(e, t);
    }, n;
  }
  function Ys(e, t, n) {
    n = Pt(-1, n), n.tag = 3;
    var r = e.type.getDerivedStateFromError;
    if (typeof r == "function") {
      var l = t.value;
      n.payload = function() {
        return r(l);
      }, n.callback = function() {
        lo(e, t);
      };
    }
    var i = e.stateNode;
    return i !== null && typeof i.componentDidCatch == "function" && (n.callback = function() {
      lo(e, t), typeof r != "function" && (qt === null ? qt = /* @__PURE__ */ new Set([this]) : qt.add(this));
      var o = t.stack;
      this.componentDidCatch(t.value, { componentStack: o !== null ? o : "" });
    }), n;
  }
  function Xs(e, t, n) {
    var r = e.pingCache;
    if (r === null) {
      r = e.pingCache = new Dd();
      var l = /* @__PURE__ */ new Set();
      r.set(t, l);
    } else l = r.get(t), l === void 0 && (l = /* @__PURE__ */ new Set(), r.set(t, l));
    l.has(n) || (l.add(n), e = Xd.bind(null, e, t, n), t.then(e, e));
  }
  function Gs(e) {
    do {
      var t;
      if ((t = e.tag === 13) && (t = e.memoizedState, t = t !== null ? t.dehydrated !== null : !0), t) return e;
      e = e.return;
    } while (e !== null);
    return null;
  }
  function Js(e, t, n, r, l) {
    return (e.mode & 1) === 0 ? (e === t ? e.flags |= 65536 : (e.flags |= 128, n.flags |= 131072, n.flags &= -52805, n.tag === 1 && (n.alternate === null ? n.tag = 17 : (t = Pt(-1, 1), t.tag = 2, Ht(n, t, 1))), n.lanes |= 1), e) : (e.flags |= 65536, e.lanes = l, e);
  }
  var Od = ee.ReactCurrentOwner, Ye = !1;
  function We(e, t, n, r) {
    t.child = e === null ? ys(t, null, n, r) : Tn(t, e.child, n, r);
  }
  function Zs(e, t, n, r, l) {
    n = n.render;
    var i = t.ref;
    return In(t, l), r = Xi(e, t, n, r, i, l), n = Gi(), e !== null && !Ye ? (t.updateQueue = e.updateQueue, t.flags &= -2053, e.lanes &= ~l, Rt(e, t, l)) : (xe && n && Ti(t), t.flags |= 1, We(e, t, r, l), t.child);
  }
  function bs(e, t, n, r, l) {
    if (e === null) {
      var i = n.type;
      return typeof i == "function" && !Co(i) && i.defaultProps === void 0 && n.compare === null && n.defaultProps === void 0 ? (t.tag = 15, t.type = i, ea(e, t, i, r, l)) : (e = Ll(n.type, null, r, t, t.mode, l), e.ref = t.ref, e.return = t, t.child = e);
    }
    if (i = e.child, (e.lanes & l) === 0) {
      var o = i.memoizedProps;
      if (n = n.compare, n = n !== null ? n : or, n(o, r) && e.ref === t.ref) return Rt(e, t, l);
    }
    return t.flags |= 1, e = Gt(i, r), e.ref = t.ref, e.return = t, t.child = e;
  }
  function ea(e, t, n, r, l) {
    if (e !== null) {
      var i = e.memoizedProps;
      if (or(i, r) && e.ref === t.ref) if (Ye = !1, t.pendingProps = r = i, (e.lanes & l) !== 0) (e.flags & 131072) !== 0 && (Ye = !0);
      else return t.lanes = e.lanes, Rt(e, t, l);
    }
    return io(e, t, n, r, l);
  }
  function ta(e, t, n) {
    var r = t.pendingProps, l = r.children, i = e !== null ? e.memoizedState : null;
    if (r.mode === "hidden") if ((t.mode & 1) === 0) t.memoizedState = { baseLanes: 0, cachePool: null, transitions: null }, me($n, nt), nt |= n;
    else {
      if ((n & 1073741824) === 0) return e = i !== null ? i.baseLanes | n : n, t.lanes = t.childLanes = 1073741824, t.memoizedState = { baseLanes: e, cachePool: null, transitions: null }, t.updateQueue = null, me($n, nt), nt |= e, null;
      t.memoizedState = { baseLanes: 0, cachePool: null, transitions: null }, r = i !== null ? i.baseLanes : n, me($n, nt), nt |= r;
    }
    else i !== null ? (r = i.baseLanes | n, t.memoizedState = null) : r = n, me($n, nt), nt |= r;
    return We(e, t, l, n), t.child;
  }
  function na(e, t) {
    var n = t.ref;
    (e === null && n !== null || e !== null && e.ref !== n) && (t.flags |= 512, t.flags |= 2097152);
  }
  function io(e, t, n, r, l) {
    var i = Ke(n) ? tn : $e.current;
    return i = zn(t, i), In(t, l), n = Xi(e, t, n, r, i, l), r = Gi(), e !== null && !Ye ? (t.updateQueue = e.updateQueue, t.flags &= -2053, e.lanes &= ~l, Rt(e, t, l)) : (xe && r && Ti(t), t.flags |= 1, We(e, t, n, l), t.child);
  }
  function ra(e, t, n, r, l) {
    if (Ke(n)) {
      var i = !0;
      nl(t);
    } else i = !1;
    if (In(t, l), t.stateNode === null) xl(e, t), Qs(t, n, r), no(t, n, r, l), r = !0;
    else if (e === null) {
      var o = t.stateNode, a = t.memoizedProps;
      o.props = a;
      var d = o.context, w = n.contextType;
      typeof w == "object" && w !== null ? w = ot(w) : (w = Ke(n) ? tn : $e.current, w = zn(t, w));
      var z = n.getDerivedStateFromProps, L = typeof z == "function" || typeof o.getSnapshotBeforeUpdate == "function";
      L || typeof o.UNSAFE_componentWillReceiveProps != "function" && typeof o.componentWillReceiveProps != "function" || (a !== r || d !== w) && qs(t, o, r, w), Wt = !1;
      var j = t.memoizedState;
      o.state = j, dl(t, r, o, l), d = t.memoizedState, a !== r || j !== d || qe.current || Wt ? (typeof z == "function" && (to(t, n, z, r), d = t.memoizedState), (a = Wt || Hs(t, n, a, r, j, d, w)) ? (L || typeof o.UNSAFE_componentWillMount != "function" && typeof o.componentWillMount != "function" || (typeof o.componentWillMount == "function" && o.componentWillMount(), typeof o.UNSAFE_componentWillMount == "function" && o.UNSAFE_componentWillMount()), typeof o.componentDidMount == "function" && (t.flags |= 4194308)) : (typeof o.componentDidMount == "function" && (t.flags |= 4194308), t.memoizedProps = r, t.memoizedState = d), o.props = r, o.state = d, o.context = w, r = a) : (typeof o.componentDidMount == "function" && (t.flags |= 4194308), r = !1);
    } else {
      o = t.stateNode, ws(e, t), a = t.memoizedProps, w = t.type === t.elementType ? a : ht(t.type, a), o.props = w, L = t.pendingProps, j = o.context, d = n.contextType, typeof d == "object" && d !== null ? d = ot(d) : (d = Ke(n) ? tn : $e.current, d = zn(t, d));
      var U = n.getDerivedStateFromProps;
      (z = typeof U == "function" || typeof o.getSnapshotBeforeUpdate == "function") || typeof o.UNSAFE_componentWillReceiveProps != "function" && typeof o.componentWillReceiveProps != "function" || (a !== L || j !== d) && qs(t, o, r, d), Wt = !1, j = t.memoizedState, o.state = j, dl(t, r, o, l);
      var V = t.memoizedState;
      a !== L || j !== V || qe.current || Wt ? (typeof U == "function" && (to(t, n, U, r), V = t.memoizedState), (w = Wt || Hs(t, n, w, r, j, V, d) || !1) ? (z || typeof o.UNSAFE_componentWillUpdate != "function" && typeof o.componentWillUpdate != "function" || (typeof o.componentWillUpdate == "function" && o.componentWillUpdate(r, V, d), typeof o.UNSAFE_componentWillUpdate == "function" && o.UNSAFE_componentWillUpdate(r, V, d)), typeof o.componentDidUpdate == "function" && (t.flags |= 4), typeof o.getSnapshotBeforeUpdate == "function" && (t.flags |= 1024)) : (typeof o.componentDidUpdate != "function" || a === e.memoizedProps && j === e.memoizedState || (t.flags |= 4), typeof o.getSnapshotBeforeUpdate != "function" || a === e.memoizedProps && j === e.memoizedState || (t.flags |= 1024), t.memoizedProps = r, t.memoizedState = V), o.props = r, o.state = V, o.context = d, r = w) : (typeof o.componentDidUpdate != "function" || a === e.memoizedProps && j === e.memoizedState || (t.flags |= 4), typeof o.getSnapshotBeforeUpdate != "function" || a === e.memoizedProps && j === e.memoizedState || (t.flags |= 1024), r = !1);
    }
    return oo(e, t, n, r, i, l);
  }
  function oo(e, t, n, r, l, i) {
    na(e, t);
    var o = (t.flags & 128) !== 0;
    if (!r && !o) return l && ss(t, n, !1), Rt(e, t, i);
    r = t.stateNode, Od.current = t;
    var a = o && typeof n.getDerivedStateFromError != "function" ? null : r.render();
    return t.flags |= 1, e !== null && o ? (t.child = Tn(t, e.child, null, i), t.child = Tn(t, null, a, i)) : We(e, t, a, i), t.memoizedState = r.state, l && ss(t, n, !0), t.child;
  }
  function la(e) {
    var t = e.stateNode;
    t.pendingContext ? os(e, t.pendingContext, t.pendingContext !== t.context) : t.context && os(e, t.context, !1), Wi(e, t.containerInfo);
  }
  function ia(e, t, n, r, l) {
    return Ln(), Oi(l), t.flags |= 256, We(e, t, n, r), t.child;
  }
  var uo = { dehydrated: null, treeContext: null, retryLane: 0 };
  function so(e) {
    return { baseLanes: e, cachePool: null, transitions: null };
  }
  function oa(e, t, n) {
    var r = t.pendingProps, l = ke.current, i = !1, o = (t.flags & 128) !== 0, a;
    if ((a = o) || (a = e !== null && e.memoizedState === null ? !1 : (l & 2) !== 0), a ? (i = !0, t.flags &= -129) : (e === null || e.memoizedState !== null) && (l |= 1), me(ke, l & 1), e === null)
      return Di(t), e = t.memoizedState, e !== null && (e = e.dehydrated, e !== null) ? ((t.mode & 1) === 0 ? t.lanes = 1 : e.data === "$!" ? t.lanes = 8 : t.lanes = 1073741824, null) : (o = r.children, e = r.fallback, i ? (r = t.mode, i = t.child, o = { mode: "hidden", children: o }, (r & 1) === 0 && i !== null ? (i.childLanes = 0, i.pendingProps = o) : i = Tl(o, r, 0, null), e = fn(e, r, n, null), i.return = t, e.return = t, i.sibling = e, t.child = i, t.child.memoizedState = so(n), t.memoizedState = uo, e) : ao(t, o));
    if (l = e.memoizedState, l !== null && (a = l.dehydrated, a !== null)) return Fd(e, t, o, r, a, l, n);
    if (i) {
      i = r.fallback, o = t.mode, l = e.child, a = l.sibling;
      var d = { mode: "hidden", children: r.children };
      return (o & 1) === 0 && t.child !== l ? (r = t.child, r.childLanes = 0, r.pendingProps = d, t.deletions = null) : (r = Gt(l, d), r.subtreeFlags = l.subtreeFlags & 14680064), a !== null ? i = Gt(a, i) : (i = fn(i, o, n, null), i.flags |= 2), i.return = t, r.return = t, r.sibling = i, t.child = r, r = i, i = t.child, o = e.child.memoizedState, o = o === null ? so(n) : { baseLanes: o.baseLanes | n, cachePool: null, transitions: o.transitions }, i.memoizedState = o, i.childLanes = e.childLanes & ~n, t.memoizedState = uo, r;
    }
    return i = e.child, e = i.sibling, r = Gt(i, { mode: "visible", children: r.children }), (t.mode & 1) === 0 && (r.lanes = n), r.return = t, r.sibling = null, e !== null && (n = t.deletions, n === null ? (t.deletions = [e], t.flags |= 16) : n.push(e)), t.child = r, t.memoizedState = null, r;
  }
  function ao(e, t) {
    return t = Tl({ mode: "visible", children: t }, e.mode, 0, null), t.return = e, e.child = t;
  }
  function wl(e, t, n, r) {
    return r !== null && Oi(r), Tn(t, e.child, null, n), e = ao(t, t.pendingProps.children), e.flags |= 2, t.memoizedState = null, e;
  }
  function Fd(e, t, n, r, l, i, o) {
    if (n)
      return t.flags & 256 ? (t.flags &= -257, r = ro(Error(s(422))), wl(e, t, o, r)) : t.memoizedState !== null ? (t.child = e.child, t.flags |= 128, null) : (i = r.fallback, l = t.mode, r = Tl({ mode: "visible", children: r.children }, l, 0, null), i = fn(i, l, o, null), i.flags |= 2, r.return = t, i.return = t, r.sibling = i, t.child = r, (t.mode & 1) !== 0 && Tn(t, e.child, null, o), t.child.memoizedState = so(o), t.memoizedState = uo, i);
    if ((t.mode & 1) === 0) return wl(e, t, o, null);
    if (l.data === "$!") {
      if (r = l.nextSibling && l.nextSibling.dataset, r) var a = r.dgst;
      return r = a, i = Error(s(419)), r = ro(i, r, void 0), wl(e, t, o, r);
    }
    if (a = (o & e.childLanes) !== 0, Ye || a) {
      if (r = Te, r !== null) {
        switch (o & -o) {
          case 4:
            l = 2;
            break;
          case 16:
            l = 8;
            break;
          case 64:
          case 128:
          case 256:
          case 512:
          case 1024:
          case 2048:
          case 4096:
          case 8192:
          case 16384:
          case 32768:
          case 65536:
          case 131072:
          case 262144:
          case 524288:
          case 1048576:
          case 2097152:
          case 4194304:
          case 8388608:
          case 16777216:
          case 33554432:
          case 67108864:
            l = 32;
            break;
          case 536870912:
            l = 268435456;
            break;
          default:
            l = 0;
        }
        l = (l & (r.suspendedLanes | o)) !== 0 ? 0 : l, l !== 0 && l !== i.retryLane && (i.retryLane = l, zt(e, l), yt(r, e, l, -1));
      }
      return Eo(), r = ro(Error(s(421))), wl(e, t, o, r);
    }
    return l.data === "$?" ? (t.flags |= 128, t.child = e.child, t = Gd.bind(null, e), l._reactRetry = t, null) : (e = i.treeContext, tt = Ut(l.nextSibling), et = t, xe = !0, pt = null, e !== null && (lt[it++] = Et, lt[it++] = Ct, lt[it++] = nn, Et = e.id, Ct = e.overflow, nn = t), t = ao(t, r.children), t.flags |= 4096, t);
  }
  function ua(e, t, n) {
    e.lanes |= t;
    var r = e.alternate;
    r !== null && (r.lanes |= t), Ai(e.return, t, n);
  }
  function co(e, t, n, r, l) {
    var i = e.memoizedState;
    i === null ? e.memoizedState = { isBackwards: t, rendering: null, renderingStartTime: 0, last: r, tail: n, tailMode: l } : (i.isBackwards = t, i.rendering = null, i.renderingStartTime = 0, i.last = r, i.tail = n, i.tailMode = l);
  }
  function sa(e, t, n) {
    var r = t.pendingProps, l = r.revealOrder, i = r.tail;
    if (We(e, t, r.children, n), r = ke.current, (r & 2) !== 0) r = r & 1 | 2, t.flags |= 128;
    else {
      if (e !== null && (e.flags & 128) !== 0) e: for (e = t.child; e !== null; ) {
        if (e.tag === 13) e.memoizedState !== null && ua(e, n, t);
        else if (e.tag === 19) ua(e, n, t);
        else if (e.child !== null) {
          e.child.return = e, e = e.child;
          continue;
        }
        if (e === t) break e;
        for (; e.sibling === null; ) {
          if (e.return === null || e.return === t) break e;
          e = e.return;
        }
        e.sibling.return = e.return, e = e.sibling;
      }
      r &= 1;
    }
    if (me(ke, r), (t.mode & 1) === 0) t.memoizedState = null;
    else switch (l) {
      case "forwards":
        for (n = t.child, l = null; n !== null; ) e = n.alternate, e !== null && fl(e) === null && (l = n), n = n.sibling;
        n = l, n === null ? (l = t.child, t.child = null) : (l = n.sibling, n.sibling = null), co(t, !1, l, n, i);
        break;
      case "backwards":
        for (n = null, l = t.child, t.child = null; l !== null; ) {
          if (e = l.alternate, e !== null && fl(e) === null) {
            t.child = l;
            break;
          }
          e = l.sibling, l.sibling = n, n = l, l = e;
        }
        co(t, !0, n, null, i);
        break;
      case "together":
        co(t, !1, null, null, void 0);
        break;
      default:
        t.memoizedState = null;
    }
    return t.child;
  }
  function xl(e, t) {
    (t.mode & 1) === 0 && e !== null && (e.alternate = null, t.alternate = null, t.flags |= 2);
  }
  function Rt(e, t, n) {
    if (e !== null && (t.dependencies = e.dependencies), sn |= t.lanes, (n & t.childLanes) === 0) return null;
    if (e !== null && t.child !== e.child) throw Error(s(153));
    if (t.child !== null) {
      for (e = t.child, n = Gt(e, e.pendingProps), t.child = n, n.return = t; e.sibling !== null; ) e = e.sibling, n = n.sibling = Gt(e, e.pendingProps), n.return = t;
      n.sibling = null;
    }
    return t.child;
  }
  function $d(e, t, n) {
    switch (t.tag) {
      case 3:
        la(t), Ln();
        break;
      case 5:
        Ss(t);
        break;
      case 1:
        Ke(t.type) && nl(t);
        break;
      case 4:
        Wi(t, t.stateNode.containerInfo);
        break;
      case 10:
        var r = t.type._context, l = t.memoizedProps.value;
        me(sl, r._currentValue), r._currentValue = l;
        break;
      case 13:
        if (r = t.memoizedState, r !== null)
          return r.dehydrated !== null ? (me(ke, ke.current & 1), t.flags |= 128, null) : (n & t.child.childLanes) !== 0 ? oa(e, t, n) : (me(ke, ke.current & 1), e = Rt(e, t, n), e !== null ? e.sibling : null);
        me(ke, ke.current & 1);
        break;
      case 19:
        if (r = (n & t.childLanes) !== 0, (e.flags & 128) !== 0) {
          if (r) return sa(e, t, n);
          t.flags |= 128;
        }
        if (l = t.memoizedState, l !== null && (l.rendering = null, l.tail = null, l.lastEffect = null), me(ke, ke.current), r) break;
        return null;
      case 22:
      case 23:
        return t.lanes = 0, ta(e, t, n);
    }
    return Rt(e, t, n);
  }
  var aa, fo, ca, da;
  aa = function(e, t) {
    for (var n = t.child; n !== null; ) {
      if (n.tag === 5 || n.tag === 6) e.appendChild(n.stateNode);
      else if (n.tag !== 4 && n.child !== null) {
        n.child.return = n, n = n.child;
        continue;
      }
      if (n === t) break;
      for (; n.sibling === null; ) {
        if (n.return === null || n.return === t) return;
        n = n.return;
      }
      n.sibling.return = n.return, n = n.sibling;
    }
  }, fo = function() {
  }, ca = function(e, t, n, r) {
    var l = e.memoizedProps;
    if (l !== r) {
      e = t.stateNode, on(kt.current);
      var i = null;
      switch (n) {
        case "input":
          l = Al(e, l), r = Al(e, r), i = [];
          break;
        case "select":
          l = B({}, l, { value: void 0 }), r = B({}, r, { value: void 0 }), i = [];
          break;
        case "textarea":
          l = Wl(e, l), r = Wl(e, r), i = [];
          break;
        default:
          typeof l.onClick != "function" && typeof r.onClick == "function" && (e.onclick = br);
      }
      Ql(n, r);
      var o;
      n = null;
      for (w in l) if (!r.hasOwnProperty(w) && l.hasOwnProperty(w) && l[w] != null) if (w === "style") {
        var a = l[w];
        for (o in a) a.hasOwnProperty(o) && (n || (n = {}), n[o] = "");
      } else w !== "dangerouslySetInnerHTML" && w !== "children" && w !== "suppressContentEditableWarning" && w !== "suppressHydrationWarning" && w !== "autoFocus" && (m.hasOwnProperty(w) ? i || (i = []) : (i = i || []).push(w, null));
      for (w in r) {
        var d = r[w];
        if (a = l != null ? l[w] : void 0, r.hasOwnProperty(w) && d !== a && (d != null || a != null)) if (w === "style") if (a) {
          for (o in a) !a.hasOwnProperty(o) || d && d.hasOwnProperty(o) || (n || (n = {}), n[o] = "");
          for (o in d) d.hasOwnProperty(o) && a[o] !== d[o] && (n || (n = {}), n[o] = d[o]);
        } else n || (i || (i = []), i.push(
          w,
          n
        )), n = d;
        else w === "dangerouslySetInnerHTML" ? (d = d ? d.__html : void 0, a = a ? a.__html : void 0, d != null && a !== d && (i = i || []).push(w, d)) : w === "children" ? typeof d != "string" && typeof d != "number" || (i = i || []).push(w, "" + d) : w !== "suppressContentEditableWarning" && w !== "suppressHydrationWarning" && (m.hasOwnProperty(w) ? (d != null && w === "onScroll" && ve("scroll", e), i || a === d || (i = [])) : (i = i || []).push(w, d));
      }
      n && (i = i || []).push("style", n);
      var w = i;
      (t.updateQueue = w) && (t.flags |= 4);
    }
  }, da = function(e, t, n, r) {
    n !== r && (t.flags |= 4);
  };
  function kr(e, t) {
    if (!xe) switch (e.tailMode) {
      case "hidden":
        t = e.tail;
        for (var n = null; t !== null; ) t.alternate !== null && (n = t), t = t.sibling;
        n === null ? e.tail = null : n.sibling = null;
        break;
      case "collapsed":
        n = e.tail;
        for (var r = null; n !== null; ) n.alternate !== null && (r = n), n = n.sibling;
        r === null ? t || e.tail === null ? e.tail = null : e.tail.sibling = null : r.sibling = null;
    }
  }
  function Ae(e) {
    var t = e.alternate !== null && e.alternate.child === e.child, n = 0, r = 0;
    if (t) for (var l = e.child; l !== null; ) n |= l.lanes | l.childLanes, r |= l.subtreeFlags & 14680064, r |= l.flags & 14680064, l.return = e, l = l.sibling;
    else for (l = e.child; l !== null; ) n |= l.lanes | l.childLanes, r |= l.subtreeFlags, r |= l.flags, l.return = e, l = l.sibling;
    return e.subtreeFlags |= r, e.childLanes = n, t;
  }
  function Ud(e, t, n) {
    var r = t.pendingProps;
    switch (Mi(t), t.tag) {
      case 2:
      case 16:
      case 15:
      case 0:
      case 11:
      case 7:
      case 8:
      case 12:
      case 9:
      case 14:
        return Ae(t), null;
      case 1:
        return Ke(t.type) && tl(), Ae(t), null;
      case 3:
        return r = t.stateNode, Dn(), ye(qe), ye($e), qi(), r.pendingContext && (r.context = r.pendingContext, r.pendingContext = null), (e === null || e.child === null) && (ol(t) ? t.flags |= 4 : e === null || e.memoizedState.isDehydrated && (t.flags & 256) === 0 || (t.flags |= 1024, pt !== null && (_o(pt), pt = null))), fo(e, t), Ae(t), null;
      case 5:
        Hi(t);
        var l = on(vr.current);
        if (n = t.type, e !== null && t.stateNode != null) ca(e, t, n, r, l), e.ref !== t.ref && (t.flags |= 512, t.flags |= 2097152);
        else {
          if (!r) {
            if (t.stateNode === null) throw Error(s(166));
            return Ae(t), null;
          }
          if (e = on(kt.current), ol(t)) {
            r = t.stateNode, n = t.type;
            var i = t.memoizedProps;
            switch (r[xt] = t, r[dr] = i, e = (t.mode & 1) !== 0, n) {
              case "dialog":
                ve("cancel", r), ve("close", r);
                break;
              case "iframe":
              case "object":
              case "embed":
                ve("load", r);
                break;
              case "video":
              case "audio":
                for (l = 0; l < sr.length; l++) ve(sr[l], r);
                break;
              case "source":
                ve("error", r);
                break;
              case "img":
              case "image":
              case "link":
                ve(
                  "error",
                  r
                ), ve("load", r);
                break;
              case "details":
                ve("toggle", r);
                break;
              case "input":
                Qo(r, i), ve("invalid", r);
                break;
              case "select":
                r._wrapperState = { wasMultiple: !!i.multiple }, ve("invalid", r);
                break;
              case "textarea":
                Yo(r, i), ve("invalid", r);
            }
            Ql(n, i), l = null;
            for (var o in i) if (i.hasOwnProperty(o)) {
              var a = i[o];
              o === "children" ? typeof a == "string" ? r.textContent !== a && (i.suppressHydrationWarning !== !0 && Zr(r.textContent, a, e), l = ["children", a]) : typeof a == "number" && r.textContent !== "" + a && (i.suppressHydrationWarning !== !0 && Zr(
                r.textContent,
                a,
                e
              ), l = ["children", "" + a]) : m.hasOwnProperty(o) && a != null && o === "onScroll" && ve("scroll", r);
            }
            switch (n) {
              case "input":
                Pr(r), Ko(r, i, !0);
                break;
              case "textarea":
                Pr(r), Go(r);
                break;
              case "select":
              case "option":
                break;
              default:
                typeof i.onClick == "function" && (r.onclick = br);
            }
            r = l, t.updateQueue = r, r !== null && (t.flags |= 4);
          } else {
            o = l.nodeType === 9 ? l : l.ownerDocument, e === "http://www.w3.org/1999/xhtml" && (e = Jo(n)), e === "http://www.w3.org/1999/xhtml" ? n === "script" ? (e = o.createElement("div"), e.innerHTML = "<script><\/script>", e = e.removeChild(e.firstChild)) : typeof r.is == "string" ? e = o.createElement(n, { is: r.is }) : (e = o.createElement(n), n === "select" && (o = e, r.multiple ? o.multiple = !0 : r.size && (o.size = r.size))) : e = o.createElementNS(e, n), e[xt] = t, e[dr] = r, aa(e, t, !1, !1), t.stateNode = e;
            e: {
              switch (o = ql(n, r), n) {
                case "dialog":
                  ve("cancel", e), ve("close", e), l = r;
                  break;
                case "iframe":
                case "object":
                case "embed":
                  ve("load", e), l = r;
                  break;
                case "video":
                case "audio":
                  for (l = 0; l < sr.length; l++) ve(sr[l], e);
                  l = r;
                  break;
                case "source":
                  ve("error", e), l = r;
                  break;
                case "img":
                case "image":
                case "link":
                  ve(
                    "error",
                    e
                  ), ve("load", e), l = r;
                  break;
                case "details":
                  ve("toggle", e), l = r;
                  break;
                case "input":
                  Qo(e, r), l = Al(e, r), ve("invalid", e);
                  break;
                case "option":
                  l = r;
                  break;
                case "select":
                  e._wrapperState = { wasMultiple: !!r.multiple }, l = B({}, r, { value: void 0 }), ve("invalid", e);
                  break;
                case "textarea":
                  Yo(e, r), l = Wl(e, r), ve("invalid", e);
                  break;
                default:
                  l = r;
              }
              Ql(n, l), a = l;
              for (i in a) if (a.hasOwnProperty(i)) {
                var d = a[i];
                i === "style" ? eu(e, d) : i === "dangerouslySetInnerHTML" ? (d = d ? d.__html : void 0, d != null && Zo(e, d)) : i === "children" ? typeof d == "string" ? (n !== "textarea" || d !== "") && Wn(e, d) : typeof d == "number" && Wn(e, "" + d) : i !== "suppressContentEditableWarning" && i !== "suppressHydrationWarning" && i !== "autoFocus" && (m.hasOwnProperty(i) ? d != null && i === "onScroll" && ve("scroll", e) : d != null && G(e, i, d, o));
              }
              switch (n) {
                case "input":
                  Pr(e), Ko(e, r, !1);
                  break;
                case "textarea":
                  Pr(e), Go(e);
                  break;
                case "option":
                  r.value != null && e.setAttribute("value", "" + ce(r.value));
                  break;
                case "select":
                  e.multiple = !!r.multiple, i = r.value, i != null ? vn(e, !!r.multiple, i, !1) : r.defaultValue != null && vn(
                    e,
                    !!r.multiple,
                    r.defaultValue,
                    !0
                  );
                  break;
                default:
                  typeof l.onClick == "function" && (e.onclick = br);
              }
              switch (n) {
                case "button":
                case "input":
                case "select":
                case "textarea":
                  r = !!r.autoFocus;
                  break e;
                case "img":
                  r = !0;
                  break e;
                default:
                  r = !1;
              }
            }
            r && (t.flags |= 4);
          }
          t.ref !== null && (t.flags |= 512, t.flags |= 2097152);
        }
        return Ae(t), null;
      case 6:
        if (e && t.stateNode != null) da(e, t, e.memoizedProps, r);
        else {
          if (typeof r != "string" && t.stateNode === null) throw Error(s(166));
          if (n = on(vr.current), on(kt.current), ol(t)) {
            if (r = t.stateNode, n = t.memoizedProps, r[xt] = t, (i = r.nodeValue !== n) && (e = et, e !== null)) switch (e.tag) {
              case 3:
                Zr(r.nodeValue, n, (e.mode & 1) !== 0);
                break;
              case 5:
                e.memoizedProps.suppressHydrationWarning !== !0 && Zr(r.nodeValue, n, (e.mode & 1) !== 0);
            }
            i && (t.flags |= 4);
          } else r = (n.nodeType === 9 ? n : n.ownerDocument).createTextNode(r), r[xt] = t, t.stateNode = r;
        }
        return Ae(t), null;
      case 13:
        if (ye(ke), r = t.memoizedState, e === null || e.memoizedState !== null && e.memoizedState.dehydrated !== null) {
          if (xe && tt !== null && (t.mode & 1) !== 0 && (t.flags & 128) === 0) hs(), Ln(), t.flags |= 98560, i = !1;
          else if (i = ol(t), r !== null && r.dehydrated !== null) {
            if (e === null) {
              if (!i) throw Error(s(318));
              if (i = t.memoizedState, i = i !== null ? i.dehydrated : null, !i) throw Error(s(317));
              i[xt] = t;
            } else Ln(), (t.flags & 128) === 0 && (t.memoizedState = null), t.flags |= 4;
            Ae(t), i = !1;
          } else pt !== null && (_o(pt), pt = null), i = !0;
          if (!i) return t.flags & 65536 ? t : null;
        }
        return (t.flags & 128) !== 0 ? (t.lanes = n, t) : (r = r !== null, r !== (e !== null && e.memoizedState !== null) && r && (t.child.flags |= 8192, (t.mode & 1) !== 0 && (e === null || (ke.current & 1) !== 0 ? Re === 0 && (Re = 3) : Eo())), t.updateQueue !== null && (t.flags |= 4), Ae(t), null);
      case 4:
        return Dn(), fo(e, t), e === null && ar(t.stateNode.containerInfo), Ae(t), null;
      case 10:
        return Ui(t.type._context), Ae(t), null;
      case 17:
        return Ke(t.type) && tl(), Ae(t), null;
      case 19:
        if (ye(ke), i = t.memoizedState, i === null) return Ae(t), null;
        if (r = (t.flags & 128) !== 0, o = i.rendering, o === null) if (r) kr(i, !1);
        else {
          if (Re !== 0 || e !== null && (e.flags & 128) !== 0) for (e = t.child; e !== null; ) {
            if (o = fl(e), o !== null) {
              for (t.flags |= 128, kr(i, !1), r = o.updateQueue, r !== null && (t.updateQueue = r, t.flags |= 4), t.subtreeFlags = 0, r = n, n = t.child; n !== null; ) i = n, e = r, i.flags &= 14680066, o = i.alternate, o === null ? (i.childLanes = 0, i.lanes = e, i.child = null, i.subtreeFlags = 0, i.memoizedProps = null, i.memoizedState = null, i.updateQueue = null, i.dependencies = null, i.stateNode = null) : (i.childLanes = o.childLanes, i.lanes = o.lanes, i.child = o.child, i.subtreeFlags = 0, i.deletions = null, i.memoizedProps = o.memoizedProps, i.memoizedState = o.memoizedState, i.updateQueue = o.updateQueue, i.type = o.type, e = o.dependencies, i.dependencies = e === null ? null : { lanes: e.lanes, firstContext: e.firstContext }), n = n.sibling;
              return me(ke, ke.current & 1 | 2), t.child;
            }
            e = e.sibling;
          }
          i.tail !== null && Ne() > Un && (t.flags |= 128, r = !0, kr(i, !1), t.lanes = 4194304);
        }
        else {
          if (!r) if (e = fl(o), e !== null) {
            if (t.flags |= 128, r = !0, n = e.updateQueue, n !== null && (t.updateQueue = n, t.flags |= 4), kr(i, !0), i.tail === null && i.tailMode === "hidden" && !o.alternate && !xe) return Ae(t), null;
          } else 2 * Ne() - i.renderingStartTime > Un && n !== 1073741824 && (t.flags |= 128, r = !0, kr(i, !1), t.lanes = 4194304);
          i.isBackwards ? (o.sibling = t.child, t.child = o) : (n = i.last, n !== null ? n.sibling = o : t.child = o, i.last = o);
        }
        return i.tail !== null ? (t = i.tail, i.rendering = t, i.tail = t.sibling, i.renderingStartTime = Ne(), t.sibling = null, n = ke.current, me(ke, r ? n & 1 | 2 : n & 1), t) : (Ae(t), null);
      case 22:
      case 23:
        return No(), r = t.memoizedState !== null, e !== null && e.memoizedState !== null !== r && (t.flags |= 8192), r && (t.mode & 1) !== 0 ? (nt & 1073741824) !== 0 && (Ae(t), t.subtreeFlags & 6 && (t.flags |= 8192)) : Ae(t), null;
      case 24:
        return null;
      case 25:
        return null;
    }
    throw Error(s(156, t.tag));
  }
  function Ad(e, t) {
    switch (Mi(t), t.tag) {
      case 1:
        return Ke(t.type) && tl(), e = t.flags, e & 65536 ? (t.flags = e & -65537 | 128, t) : null;
      case 3:
        return Dn(), ye(qe), ye($e), qi(), e = t.flags, (e & 65536) !== 0 && (e & 128) === 0 ? (t.flags = e & -65537 | 128, t) : null;
      case 5:
        return Hi(t), null;
      case 13:
        if (ye(ke), e = t.memoizedState, e !== null && e.dehydrated !== null) {
          if (t.alternate === null) throw Error(s(340));
          Ln();
        }
        return e = t.flags, e & 65536 ? (t.flags = e & -65537 | 128, t) : null;
      case 19:
        return ye(ke), null;
      case 4:
        return Dn(), null;
      case 10:
        return Ui(t.type._context), null;
      case 22:
      case 23:
        return No(), null;
      case 24:
        return null;
      default:
        return null;
    }
  }
  var kl = !1, Be = !1, Bd = typeof WeakSet == "function" ? WeakSet : Set, A = null;
  function Fn(e, t) {
    var n = e.ref;
    if (n !== null) if (typeof n == "function") try {
      n(null);
    } catch (r) {
      je(e, t, r);
    }
    else n.current = null;
  }
  function po(e, t, n) {
    try {
      n();
    } catch (r) {
      je(e, t, r);
    }
  }
  var fa = !1;
  function Vd(e, t) {
    if (ji = Br, e = Hu(), vi(e)) {
      if ("selectionStart" in e) var n = { start: e.selectionStart, end: e.selectionEnd };
      else e: {
        n = (n = e.ownerDocument) && n.defaultView || window;
        var r = n.getSelection && n.getSelection();
        if (r && r.rangeCount !== 0) {
          n = r.anchorNode;
          var l = r.anchorOffset, i = r.focusNode;
          r = r.focusOffset;
          try {
            n.nodeType, i.nodeType;
          } catch {
            n = null;
            break e;
          }
          var o = 0, a = -1, d = -1, w = 0, z = 0, L = e, j = null;
          t: for (; ; ) {
            for (var U; L !== n || l !== 0 && L.nodeType !== 3 || (a = o + l), L !== i || r !== 0 && L.nodeType !== 3 || (d = o + r), L.nodeType === 3 && (o += L.nodeValue.length), (U = L.firstChild) !== null; )
              j = L, L = U;
            for (; ; ) {
              if (L === e) break t;
              if (j === n && ++w === l && (a = o), j === i && ++z === r && (d = o), (U = L.nextSibling) !== null) break;
              L = j, j = L.parentNode;
            }
            L = U;
          }
          n = a === -1 || d === -1 ? null : { start: a, end: d };
        } else n = null;
      }
      n = n || { start: 0, end: 0 };
    } else n = null;
    for (Ni = { focusedElem: e, selectionRange: n }, Br = !1, A = t; A !== null; ) if (t = A, e = t.child, (t.subtreeFlags & 1028) !== 0 && e !== null) e.return = t, A = e;
    else for (; A !== null; ) {
      t = A;
      try {
        var V = t.alternate;
        if ((t.flags & 1024) !== 0) switch (t.tag) {
          case 0:
          case 11:
          case 15:
            break;
          case 1:
            if (V !== null) {
              var H = V.memoizedProps, Ee = V.memoizedState, v = t.stateNode, p = v.getSnapshotBeforeUpdate(t.elementType === t.type ? H : ht(t.type, H), Ee);
              v.__reactInternalSnapshotBeforeUpdate = p;
            }
            break;
          case 3:
            var g = t.stateNode.containerInfo;
            g.nodeType === 1 ? g.textContent = "" : g.nodeType === 9 && g.documentElement && g.removeChild(g.documentElement);
            break;
          case 5:
          case 6:
          case 4:
          case 17:
            break;
          default:
            throw Error(s(163));
        }
      } catch (T) {
        je(t, t.return, T);
      }
      if (e = t.sibling, e !== null) {
        e.return = t.return, A = e;
        break;
      }
      A = t.return;
    }
    return V = fa, fa = !1, V;
  }
  function Sr(e, t, n) {
    var r = t.updateQueue;
    if (r = r !== null ? r.lastEffect : null, r !== null) {
      var l = r = r.next;
      do {
        if ((l.tag & e) === e) {
          var i = l.destroy;
          l.destroy = void 0, i !== void 0 && po(t, n, i);
        }
        l = l.next;
      } while (l !== r);
    }
  }
  function Sl(e, t) {
    if (t = t.updateQueue, t = t !== null ? t.lastEffect : null, t !== null) {
      var n = t = t.next;
      do {
        if ((n.tag & e) === e) {
          var r = n.create;
          n.destroy = r();
        }
        n = n.next;
      } while (n !== t);
    }
  }
  function ho(e) {
    var t = e.ref;
    if (t !== null) {
      var n = e.stateNode;
      switch (e.tag) {
        case 5:
          e = n;
          break;
        default:
          e = n;
      }
      typeof t == "function" ? t(e) : t.current = e;
    }
  }
  function pa(e) {
    var t = e.alternate;
    t !== null && (e.alternate = null, pa(t)), e.child = null, e.deletions = null, e.sibling = null, e.tag === 5 && (t = e.stateNode, t !== null && (delete t[xt], delete t[dr], delete t[Pi], delete t[jd], delete t[Nd])), e.stateNode = null, e.return = null, e.dependencies = null, e.memoizedProps = null, e.memoizedState = null, e.pendingProps = null, e.stateNode = null, e.updateQueue = null;
  }
  function ha(e) {
    return e.tag === 5 || e.tag === 3 || e.tag === 4;
  }
  function ma(e) {
    e: for (; ; ) {
      for (; e.sibling === null; ) {
        if (e.return === null || ha(e.return)) return null;
        e = e.return;
      }
      for (e.sibling.return = e.return, e = e.sibling; e.tag !== 5 && e.tag !== 6 && e.tag !== 18; ) {
        if (e.flags & 2 || e.child === null || e.tag === 4) continue e;
        e.child.return = e, e = e.child;
      }
      if (!(e.flags & 2)) return e.stateNode;
    }
  }
  function mo(e, t, n) {
    var r = e.tag;
    if (r === 5 || r === 6) e = e.stateNode, t ? n.nodeType === 8 ? n.parentNode.insertBefore(e, t) : n.insertBefore(e, t) : (n.nodeType === 8 ? (t = n.parentNode, t.insertBefore(e, n)) : (t = n, t.appendChild(e)), n = n._reactRootContainer, n != null || t.onclick !== null || (t.onclick = br));
    else if (r !== 4 && (e = e.child, e !== null)) for (mo(e, t, n), e = e.sibling; e !== null; ) mo(e, t, n), e = e.sibling;
  }
  function vo(e, t, n) {
    var r = e.tag;
    if (r === 5 || r === 6) e = e.stateNode, t ? n.insertBefore(e, t) : n.appendChild(e);
    else if (r !== 4 && (e = e.child, e !== null)) for (vo(e, t, n), e = e.sibling; e !== null; ) vo(e, t, n), e = e.sibling;
  }
  var Ie = null, mt = !1;
  function Qt(e, t, n) {
    for (n = n.child; n !== null; ) va(e, t, n), n = n.sibling;
  }
  function va(e, t, n) {
    if (wt && typeof wt.onCommitFiberUnmount == "function") try {
      wt.onCommitFiberUnmount(Dr, n);
    } catch {
    }
    switch (n.tag) {
      case 5:
        Be || Fn(n, t);
      case 6:
        var r = Ie, l = mt;
        Ie = null, Qt(e, t, n), Ie = r, mt = l, Ie !== null && (mt ? (e = Ie, n = n.stateNode, e.nodeType === 8 ? e.parentNode.removeChild(n) : e.removeChild(n)) : Ie.removeChild(n.stateNode));
        break;
      case 18:
        Ie !== null && (mt ? (e = Ie, n = n.stateNode, e.nodeType === 8 ? zi(e.parentNode, n) : e.nodeType === 1 && zi(e, n), er(e)) : zi(Ie, n.stateNode));
        break;
      case 4:
        r = Ie, l = mt, Ie = n.stateNode.containerInfo, mt = !0, Qt(e, t, n), Ie = r, mt = l;
        break;
      case 0:
      case 11:
      case 14:
      case 15:
        if (!Be && (r = n.updateQueue, r !== null && (r = r.lastEffect, r !== null))) {
          l = r = r.next;
          do {
            var i = l, o = i.destroy;
            i = i.tag, o !== void 0 && ((i & 2) !== 0 || (i & 4) !== 0) && po(n, t, o), l = l.next;
          } while (l !== r);
        }
        Qt(e, t, n);
        break;
      case 1:
        if (!Be && (Fn(n, t), r = n.stateNode, typeof r.componentWillUnmount == "function")) try {
          r.props = n.memoizedProps, r.state = n.memoizedState, r.componentWillUnmount();
        } catch (a) {
          je(n, t, a);
        }
        Qt(e, t, n);
        break;
      case 21:
        Qt(e, t, n);
        break;
      case 22:
        n.mode & 1 ? (Be = (r = Be) || n.memoizedState !== null, Qt(e, t, n), Be = r) : Qt(e, t, n);
        break;
      default:
        Qt(e, t, n);
    }
  }
  function ya(e) {
    var t = e.updateQueue;
    if (t !== null) {
      e.updateQueue = null;
      var n = e.stateNode;
      n === null && (n = e.stateNode = new Bd()), t.forEach(function(r) {
        var l = Jd.bind(null, e, r);
        n.has(r) || (n.add(r), r.then(l, l));
      });
    }
  }
  function vt(e, t) {
    var n = t.deletions;
    if (n !== null) for (var r = 0; r < n.length; r++) {
      var l = n[r];
      try {
        var i = e, o = t, a = o;
        e: for (; a !== null; ) {
          switch (a.tag) {
            case 5:
              Ie = a.stateNode, mt = !1;
              break e;
            case 3:
              Ie = a.stateNode.containerInfo, mt = !0;
              break e;
            case 4:
              Ie = a.stateNode.containerInfo, mt = !0;
              break e;
          }
          a = a.return;
        }
        if (Ie === null) throw Error(s(160));
        va(i, o, l), Ie = null, mt = !1;
        var d = l.alternate;
        d !== null && (d.return = null), l.return = null;
      } catch (w) {
        je(l, t, w);
      }
    }
    if (t.subtreeFlags & 12854) for (t = t.child; t !== null; ) ga(t, e), t = t.sibling;
  }
  function ga(e, t) {
    var n = e.alternate, r = e.flags;
    switch (e.tag) {
      case 0:
      case 11:
      case 14:
      case 15:
        if (vt(t, e), _t(e), r & 4) {
          try {
            Sr(3, e, e.return), Sl(3, e);
          } catch (H) {
            je(e, e.return, H);
          }
          try {
            Sr(5, e, e.return);
          } catch (H) {
            je(e, e.return, H);
          }
        }
        break;
      case 1:
        vt(t, e), _t(e), r & 512 && n !== null && Fn(n, n.return);
        break;
      case 5:
        if (vt(t, e), _t(e), r & 512 && n !== null && Fn(n, n.return), e.flags & 32) {
          var l = e.stateNode;
          try {
            Wn(l, "");
          } catch (H) {
            je(e, e.return, H);
          }
        }
        if (r & 4 && (l = e.stateNode, l != null)) {
          var i = e.memoizedProps, o = n !== null ? n.memoizedProps : i, a = e.type, d = e.updateQueue;
          if (e.updateQueue = null, d !== null) try {
            a === "input" && i.type === "radio" && i.name != null && qo(l, i), ql(a, o);
            var w = ql(a, i);
            for (o = 0; o < d.length; o += 2) {
              var z = d[o], L = d[o + 1];
              z === "style" ? eu(l, L) : z === "dangerouslySetInnerHTML" ? Zo(l, L) : z === "children" ? Wn(l, L) : G(l, z, L, w);
            }
            switch (a) {
              case "input":
                Bl(l, i);
                break;
              case "textarea":
                Xo(l, i);
                break;
              case "select":
                var j = l._wrapperState.wasMultiple;
                l._wrapperState.wasMultiple = !!i.multiple;
                var U = i.value;
                U != null ? vn(l, !!i.multiple, U, !1) : j !== !!i.multiple && (i.defaultValue != null ? vn(
                  l,
                  !!i.multiple,
                  i.defaultValue,
                  !0
                ) : vn(l, !!i.multiple, i.multiple ? [] : "", !1));
            }
            l[dr] = i;
          } catch (H) {
            je(e, e.return, H);
          }
        }
        break;
      case 6:
        if (vt(t, e), _t(e), r & 4) {
          if (e.stateNode === null) throw Error(s(162));
          l = e.stateNode, i = e.memoizedProps;
          try {
            l.nodeValue = i;
          } catch (H) {
            je(e, e.return, H);
          }
        }
        break;
      case 3:
        if (vt(t, e), _t(e), r & 4 && n !== null && n.memoizedState.isDehydrated) try {
          er(t.containerInfo);
        } catch (H) {
          je(e, e.return, H);
        }
        break;
      case 4:
        vt(t, e), _t(e);
        break;
      case 13:
        vt(t, e), _t(e), l = e.child, l.flags & 8192 && (i = l.memoizedState !== null, l.stateNode.isHidden = i, !i || l.alternate !== null && l.alternate.memoizedState !== null || (wo = Ne())), r & 4 && ya(e);
        break;
      case 22:
        if (z = n !== null && n.memoizedState !== null, e.mode & 1 ? (Be = (w = Be) || z, vt(t, e), Be = w) : vt(t, e), _t(e), r & 8192) {
          if (w = e.memoizedState !== null, (e.stateNode.isHidden = w) && !z && (e.mode & 1) !== 0) for (A = e, z = e.child; z !== null; ) {
            for (L = A = z; A !== null; ) {
              switch (j = A, U = j.child, j.tag) {
                case 0:
                case 11:
                case 14:
                case 15:
                  Sr(4, j, j.return);
                  break;
                case 1:
                  Fn(j, j.return);
                  var V = j.stateNode;
                  if (typeof V.componentWillUnmount == "function") {
                    r = j, n = j.return;
                    try {
                      t = r, V.props = t.memoizedProps, V.state = t.memoizedState, V.componentWillUnmount();
                    } catch (H) {
                      je(r, n, H);
                    }
                  }
                  break;
                case 5:
                  Fn(j, j.return);
                  break;
                case 22:
                  if (j.memoizedState !== null) {
                    ka(L);
                    continue;
                  }
              }
              U !== null ? (U.return = j, A = U) : ka(L);
            }
            z = z.sibling;
          }
          e: for (z = null, L = e; ; ) {
            if (L.tag === 5) {
              if (z === null) {
                z = L;
                try {
                  l = L.stateNode, w ? (i = l.style, typeof i.setProperty == "function" ? i.setProperty("display", "none", "important") : i.display = "none") : (a = L.stateNode, d = L.memoizedProps.style, o = d != null && d.hasOwnProperty("display") ? d.display : null, a.style.display = bo("display", o));
                } catch (H) {
                  je(e, e.return, H);
                }
              }
            } else if (L.tag === 6) {
              if (z === null) try {
                L.stateNode.nodeValue = w ? "" : L.memoizedProps;
              } catch (H) {
                je(e, e.return, H);
              }
            } else if ((L.tag !== 22 && L.tag !== 23 || L.memoizedState === null || L === e) && L.child !== null) {
              L.child.return = L, L = L.child;
              continue;
            }
            if (L === e) break e;
            for (; L.sibling === null; ) {
              if (L.return === null || L.return === e) break e;
              z === L && (z = null), L = L.return;
            }
            z === L && (z = null), L.sibling.return = L.return, L = L.sibling;
          }
        }
        break;
      case 19:
        vt(t, e), _t(e), r & 4 && ya(e);
        break;
      case 21:
        break;
      default:
        vt(
          t,
          e
        ), _t(e);
    }
  }
  function _t(e) {
    var t = e.flags;
    if (t & 2) {
      try {
        e: {
          for (var n = e.return; n !== null; ) {
            if (ha(n)) {
              var r = n;
              break e;
            }
            n = n.return;
          }
          throw Error(s(160));
        }
        switch (r.tag) {
          case 5:
            var l = r.stateNode;
            r.flags & 32 && (Wn(l, ""), r.flags &= -33);
            var i = ma(e);
            vo(e, i, l);
            break;
          case 3:
          case 4:
            var o = r.stateNode.containerInfo, a = ma(e);
            mo(e, a, o);
            break;
          default:
            throw Error(s(161));
        }
      } catch (d) {
        je(e, e.return, d);
      }
      e.flags &= -3;
    }
    t & 4096 && (e.flags &= -4097);
  }
  function Wd(e, t, n) {
    A = e, wa(e);
  }
  function wa(e, t, n) {
    for (var r = (e.mode & 1) !== 0; A !== null; ) {
      var l = A, i = l.child;
      if (l.tag === 22 && r) {
        var o = l.memoizedState !== null || kl;
        if (!o) {
          var a = l.alternate, d = a !== null && a.memoizedState !== null || Be;
          a = kl;
          var w = Be;
          if (kl = o, (Be = d) && !w) for (A = l; A !== null; ) o = A, d = o.child, o.tag === 22 && o.memoizedState !== null ? Sa(l) : d !== null ? (d.return = o, A = d) : Sa(l);
          for (; i !== null; ) A = i, wa(i), i = i.sibling;
          A = l, kl = a, Be = w;
        }
        xa(e);
      } else (l.subtreeFlags & 8772) !== 0 && i !== null ? (i.return = l, A = i) : xa(e);
    }
  }
  function xa(e) {
    for (; A !== null; ) {
      var t = A;
      if ((t.flags & 8772) !== 0) {
        var n = t.alternate;
        try {
          if ((t.flags & 8772) !== 0) switch (t.tag) {
            case 0:
            case 11:
            case 15:
              Be || Sl(5, t);
              break;
            case 1:
              var r = t.stateNode;
              if (t.flags & 4 && !Be) if (n === null) r.componentDidMount();
              else {
                var l = t.elementType === t.type ? n.memoizedProps : ht(t.type, n.memoizedProps);
                r.componentDidUpdate(l, n.memoizedState, r.__reactInternalSnapshotBeforeUpdate);
              }
              var i = t.updateQueue;
              i !== null && ks(t, i, r);
              break;
            case 3:
              var o = t.updateQueue;
              if (o !== null) {
                if (n = null, t.child !== null) switch (t.child.tag) {
                  case 5:
                    n = t.child.stateNode;
                    break;
                  case 1:
                    n = t.child.stateNode;
                }
                ks(t, o, n);
              }
              break;
            case 5:
              var a = t.stateNode;
              if (n === null && t.flags & 4) {
                n = a;
                var d = t.memoizedProps;
                switch (t.type) {
                  case "button":
                  case "input":
                  case "select":
                  case "textarea":
                    d.autoFocus && n.focus();
                    break;
                  case "img":
                    d.src && (n.src = d.src);
                }
              }
              break;
            case 6:
              break;
            case 4:
              break;
            case 12:
              break;
            case 13:
              if (t.memoizedState === null) {
                var w = t.alternate;
                if (w !== null) {
                  var z = w.memoizedState;
                  if (z !== null) {
                    var L = z.dehydrated;
                    L !== null && er(L);
                  }
                }
              }
              break;
            case 19:
            case 17:
            case 21:
            case 22:
            case 23:
            case 25:
              break;
            default:
              throw Error(s(163));
          }
          Be || t.flags & 512 && ho(t);
        } catch (j) {
          je(t, t.return, j);
        }
      }
      if (t === e) {
        A = null;
        break;
      }
      if (n = t.sibling, n !== null) {
        n.return = t.return, A = n;
        break;
      }
      A = t.return;
    }
  }
  function ka(e) {
    for (; A !== null; ) {
      var t = A;
      if (t === e) {
        A = null;
        break;
      }
      var n = t.sibling;
      if (n !== null) {
        n.return = t.return, A = n;
        break;
      }
      A = t.return;
    }
  }
  function Sa(e) {
    for (; A !== null; ) {
      var t = A;
      try {
        switch (t.tag) {
          case 0:
          case 11:
          case 15:
            var n = t.return;
            try {
              Sl(4, t);
            } catch (d) {
              je(t, n, d);
            }
            break;
          case 1:
            var r = t.stateNode;
            if (typeof r.componentDidMount == "function") {
              var l = t.return;
              try {
                r.componentDidMount();
              } catch (d) {
                je(t, l, d);
              }
            }
            var i = t.return;
            try {
              ho(t);
            } catch (d) {
              je(t, i, d);
            }
            break;
          case 5:
            var o = t.return;
            try {
              ho(t);
            } catch (d) {
              je(t, o, d);
            }
        }
      } catch (d) {
        je(t, t.return, d);
      }
      if (t === e) {
        A = null;
        break;
      }
      var a = t.sibling;
      if (a !== null) {
        a.return = t.return, A = a;
        break;
      }
      A = t.return;
    }
  }
  var Hd = Math.ceil, _l = ee.ReactCurrentDispatcher, yo = ee.ReactCurrentOwner, st = ee.ReactCurrentBatchConfig, ie = 0, Te = null, ze = null, De = 0, nt = 0, $n = At(0), Re = 0, _r = null, sn = 0, jl = 0, go = 0, jr = null, Xe = null, wo = 0, Un = 1 / 0, Lt = null, Nl = !1, xo = null, qt = null, El = !1, Kt = null, Cl = 0, Nr = 0, ko = null, zl = -1, Pl = 0;
  function He() {
    return (ie & 6) !== 0 ? Ne() : zl !== -1 ? zl : zl = Ne();
  }
  function Yt(e) {
    return (e.mode & 1) === 0 ? 1 : (ie & 2) !== 0 && De !== 0 ? De & -De : Cd.transition !== null ? (Pl === 0 && (Pl = mu()), Pl) : (e = de, e !== 0 || (e = window.event, e = e === void 0 ? 16 : ju(e.type)), e);
  }
  function yt(e, t, n, r) {
    if (50 < Nr) throw Nr = 0, ko = null, Error(s(185));
    Xn(e, n, r), ((ie & 2) === 0 || e !== Te) && (e === Te && ((ie & 2) === 0 && (jl |= n), Re === 4 && Xt(e, De)), Ge(e, r), n === 1 && ie === 0 && (t.mode & 1) === 0 && (Un = Ne() + 500, rl && Vt()));
  }
  function Ge(e, t) {
    var n = e.callbackNode;
    Cc(e, t);
    var r = $r(e, e === Te ? De : 0);
    if (r === 0) n !== null && fu(n), e.callbackNode = null, e.callbackPriority = 0;
    else if (t = r & -r, e.callbackPriority !== t) {
      if (n != null && fu(n), t === 1) e.tag === 0 ? Ed(ja.bind(null, e)) : as(ja.bind(null, e)), Sd(function() {
        (ie & 6) === 0 && Vt();
      }), n = null;
      else {
        switch (vu(r)) {
          case 1:
            n = bl;
            break;
          case 4:
            n = pu;
            break;
          case 16:
            n = Ir;
            break;
          case 536870912:
            n = hu;
            break;
          default:
            n = Ir;
        }
        n = Ta(n, _a.bind(null, e));
      }
      e.callbackPriority = t, e.callbackNode = n;
    }
  }
  function _a(e, t) {
    if (zl = -1, Pl = 0, (ie & 6) !== 0) throw Error(s(327));
    var n = e.callbackNode;
    if (An() && e.callbackNode !== n) return null;
    var r = $r(e, e === Te ? De : 0);
    if (r === 0) return null;
    if ((r & 30) !== 0 || (r & e.expiredLanes) !== 0 || t) t = Rl(e, r);
    else {
      t = r;
      var l = ie;
      ie |= 2;
      var i = Ea();
      (Te !== e || De !== t) && (Lt = null, Un = Ne() + 500, cn(e, t));
      do
        try {
          Kd();
          break;
        } catch (a) {
          Na(e, a);
        }
      while (!0);
      $i(), _l.current = i, ie = l, ze !== null ? t = 0 : (Te = null, De = 0, t = Re);
    }
    if (t !== 0) {
      if (t === 2 && (l = ei(e), l !== 0 && (r = l, t = So(e, l))), t === 1) throw n = _r, cn(e, 0), Xt(e, r), Ge(e, Ne()), n;
      if (t === 6) Xt(e, r);
      else {
        if (l = e.current.alternate, (r & 30) === 0 && !Qd(l) && (t = Rl(e, r), t === 2 && (i = ei(e), i !== 0 && (r = i, t = So(e, i))), t === 1)) throw n = _r, cn(e, 0), Xt(e, r), Ge(e, Ne()), n;
        switch (e.finishedWork = l, e.finishedLanes = r, t) {
          case 0:
          case 1:
            throw Error(s(345));
          case 2:
            dn(e, Xe, Lt);
            break;
          case 3:
            if (Xt(e, r), (r & 130023424) === r && (t = wo + 500 - Ne(), 10 < t)) {
              if ($r(e, 0) !== 0) break;
              if (l = e.suspendedLanes, (l & r) !== r) {
                He(), e.pingedLanes |= e.suspendedLanes & l;
                break;
              }
              e.timeoutHandle = Ci(dn.bind(null, e, Xe, Lt), t);
              break;
            }
            dn(e, Xe, Lt);
            break;
          case 4:
            if (Xt(e, r), (r & 4194240) === r) break;
            for (t = e.eventTimes, l = -1; 0 < r; ) {
              var o = 31 - dt(r);
              i = 1 << o, o = t[o], o > l && (l = o), r &= ~i;
            }
            if (r = l, r = Ne() - r, r = (120 > r ? 120 : 480 > r ? 480 : 1080 > r ? 1080 : 1920 > r ? 1920 : 3e3 > r ? 3e3 : 4320 > r ? 4320 : 1960 * Hd(r / 1960)) - r, 10 < r) {
              e.timeoutHandle = Ci(dn.bind(null, e, Xe, Lt), r);
              break;
            }
            dn(e, Xe, Lt);
            break;
          case 5:
            dn(e, Xe, Lt);
            break;
          default:
            throw Error(s(329));
        }
      }
    }
    return Ge(e, Ne()), e.callbackNode === n ? _a.bind(null, e) : null;
  }
  function So(e, t) {
    var n = jr;
    return e.current.memoizedState.isDehydrated && (cn(e, t).flags |= 256), e = Rl(e, t), e !== 2 && (t = Xe, Xe = n, t !== null && _o(t)), e;
  }
  function _o(e) {
    Xe === null ? Xe = e : Xe.push.apply(Xe, e);
  }
  function Qd(e) {
    for (var t = e; ; ) {
      if (t.flags & 16384) {
        var n = t.updateQueue;
        if (n !== null && (n = n.stores, n !== null)) for (var r = 0; r < n.length; r++) {
          var l = n[r], i = l.getSnapshot;
          l = l.value;
          try {
            if (!ft(i(), l)) return !1;
          } catch {
            return !1;
          }
        }
      }
      if (n = t.child, t.subtreeFlags & 16384 && n !== null) n.return = t, t = n;
      else {
        if (t === e) break;
        for (; t.sibling === null; ) {
          if (t.return === null || t.return === e) return !0;
          t = t.return;
        }
        t.sibling.return = t.return, t = t.sibling;
      }
    }
    return !0;
  }
  function Xt(e, t) {
    for (t &= ~go, t &= ~jl, e.suspendedLanes |= t, e.pingedLanes &= ~t, e = e.expirationTimes; 0 < t; ) {
      var n = 31 - dt(t), r = 1 << n;
      e[n] = -1, t &= ~r;
    }
  }
  function ja(e) {
    if ((ie & 6) !== 0) throw Error(s(327));
    An();
    var t = $r(e, 0);
    if ((t & 1) === 0) return Ge(e, Ne()), null;
    var n = Rl(e, t);
    if (e.tag !== 0 && n === 2) {
      var r = ei(e);
      r !== 0 && (t = r, n = So(e, r));
    }
    if (n === 1) throw n = _r, cn(e, 0), Xt(e, t), Ge(e, Ne()), n;
    if (n === 6) throw Error(s(345));
    return e.finishedWork = e.current.alternate, e.finishedLanes = t, dn(e, Xe, Lt), Ge(e, Ne()), null;
  }
  function jo(e, t) {
    var n = ie;
    ie |= 1;
    try {
      return e(t);
    } finally {
      ie = n, ie === 0 && (Un = Ne() + 500, rl && Vt());
    }
  }
  function an(e) {
    Kt !== null && Kt.tag === 0 && (ie & 6) === 0 && An();
    var t = ie;
    ie |= 1;
    var n = st.transition, r = de;
    try {
      if (st.transition = null, de = 1, e) return e();
    } finally {
      de = r, st.transition = n, ie = t, (ie & 6) === 0 && Vt();
    }
  }
  function No() {
    nt = $n.current, ye($n);
  }
  function cn(e, t) {
    e.finishedWork = null, e.finishedLanes = 0;
    var n = e.timeoutHandle;
    if (n !== -1 && (e.timeoutHandle = -1, kd(n)), ze !== null) for (n = ze.return; n !== null; ) {
      var r = n;
      switch (Mi(r), r.tag) {
        case 1:
          r = r.type.childContextTypes, r != null && tl();
          break;
        case 3:
          Dn(), ye(qe), ye($e), qi();
          break;
        case 5:
          Hi(r);
          break;
        case 4:
          Dn();
          break;
        case 13:
          ye(ke);
          break;
        case 19:
          ye(ke);
          break;
        case 10:
          Ui(r.type._context);
          break;
        case 22:
        case 23:
          No();
      }
      n = n.return;
    }
    if (Te = e, ze = e = Gt(e.current, null), De = nt = t, Re = 0, _r = null, go = jl = sn = 0, Xe = jr = null, ln !== null) {
      for (t = 0; t < ln.length; t++) if (n = ln[t], r = n.interleaved, r !== null) {
        n.interleaved = null;
        var l = r.next, i = n.pending;
        if (i !== null) {
          var o = i.next;
          i.next = l, r.next = o;
        }
        n.pending = r;
      }
      ln = null;
    }
    return e;
  }
  function Na(e, t) {
    do {
      var n = ze;
      try {
        if ($i(), pl.current = yl, hl) {
          for (var r = Se.memoizedState; r !== null; ) {
            var l = r.queue;
            l !== null && (l.pending = null), r = r.next;
          }
          hl = !1;
        }
        if (un = 0, Le = Pe = Se = null, yr = !1, gr = 0, yo.current = null, n === null || n.return === null) {
          Re = 1, _r = t, ze = null;
          break;
        }
        e: {
          var i = e, o = n.return, a = n, d = t;
          if (t = De, a.flags |= 32768, d !== null && typeof d == "object" && typeof d.then == "function") {
            var w = d, z = a, L = z.tag;
            if ((z.mode & 1) === 0 && (L === 0 || L === 11 || L === 15)) {
              var j = z.alternate;
              j ? (z.updateQueue = j.updateQueue, z.memoizedState = j.memoizedState, z.lanes = j.lanes) : (z.updateQueue = null, z.memoizedState = null);
            }
            var U = Gs(o);
            if (U !== null) {
              U.flags &= -257, Js(U, o, a, i, t), U.mode & 1 && Xs(i, w, t), t = U, d = w;
              var V = t.updateQueue;
              if (V === null) {
                var H = /* @__PURE__ */ new Set();
                H.add(d), t.updateQueue = H;
              } else V.add(d);
              break e;
            } else {
              if ((t & 1) === 0) {
                Xs(i, w, t), Eo();
                break e;
              }
              d = Error(s(426));
            }
          } else if (xe && a.mode & 1) {
            var Ee = Gs(o);
            if (Ee !== null) {
              (Ee.flags & 65536) === 0 && (Ee.flags |= 256), Js(Ee, o, a, i, t), Oi(On(d, a));
              break e;
            }
          }
          i = d = On(d, a), Re !== 4 && (Re = 2), jr === null ? jr = [i] : jr.push(i), i = o;
          do {
            switch (i.tag) {
              case 3:
                i.flags |= 65536, t &= -t, i.lanes |= t;
                var v = Ks(i, d, t);
                xs(i, v);
                break e;
              case 1:
                a = d;
                var p = i.type, g = i.stateNode;
                if ((i.flags & 128) === 0 && (typeof p.getDerivedStateFromError == "function" || g !== null && typeof g.componentDidCatch == "function" && (qt === null || !qt.has(g)))) {
                  i.flags |= 65536, t &= -t, i.lanes |= t;
                  var T = Ys(i, a, t);
                  xs(i, T);
                  break e;
                }
            }
            i = i.return;
          } while (i !== null);
        }
        za(n);
      } catch (Q) {
        t = Q, ze === n && n !== null && (ze = n = n.return);
        continue;
      }
      break;
    } while (!0);
  }
  function Ea() {
    var e = _l.current;
    return _l.current = yl, e === null ? yl : e;
  }
  function Eo() {
    (Re === 0 || Re === 3 || Re === 2) && (Re = 4), Te === null || (sn & 268435455) === 0 && (jl & 268435455) === 0 || Xt(Te, De);
  }
  function Rl(e, t) {
    var n = ie;
    ie |= 2;
    var r = Ea();
    (Te !== e || De !== t) && (Lt = null, cn(e, t));
    do
      try {
        qd();
        break;
      } catch (l) {
        Na(e, l);
      }
    while (!0);
    if ($i(), ie = n, _l.current = r, ze !== null) throw Error(s(261));
    return Te = null, De = 0, Re;
  }
  function qd() {
    for (; ze !== null; ) Ca(ze);
  }
  function Kd() {
    for (; ze !== null && !gc(); ) Ca(ze);
  }
  function Ca(e) {
    var t = La(e.alternate, e, nt);
    e.memoizedProps = e.pendingProps, t === null ? za(e) : ze = t, yo.current = null;
  }
  function za(e) {
    var t = e;
    do {
      var n = t.alternate;
      if (e = t.return, (t.flags & 32768) === 0) {
        if (n = Ud(n, t, nt), n !== null) {
          ze = n;
          return;
        }
      } else {
        if (n = Ad(n, t), n !== null) {
          n.flags &= 32767, ze = n;
          return;
        }
        if (e !== null) e.flags |= 32768, e.subtreeFlags = 0, e.deletions = null;
        else {
          Re = 6, ze = null;
          return;
        }
      }
      if (t = t.sibling, t !== null) {
        ze = t;
        return;
      }
      ze = t = e;
    } while (t !== null);
    Re === 0 && (Re = 5);
  }
  function dn(e, t, n) {
    var r = de, l = st.transition;
    try {
      st.transition = null, de = 1, Yd(e, t, n, r);
    } finally {
      st.transition = l, de = r;
    }
    return null;
  }
  function Yd(e, t, n, r) {
    do
      An();
    while (Kt !== null);
    if ((ie & 6) !== 0) throw Error(s(327));
    n = e.finishedWork;
    var l = e.finishedLanes;
    if (n === null) return null;
    if (e.finishedWork = null, e.finishedLanes = 0, n === e.current) throw Error(s(177));
    e.callbackNode = null, e.callbackPriority = 0;
    var i = n.lanes | n.childLanes;
    if (zc(e, i), e === Te && (ze = Te = null, De = 0), (n.subtreeFlags & 2064) === 0 && (n.flags & 2064) === 0 || El || (El = !0, Ta(Ir, function() {
      return An(), null;
    })), i = (n.flags & 15990) !== 0, (n.subtreeFlags & 15990) !== 0 || i) {
      i = st.transition, st.transition = null;
      var o = de;
      de = 1;
      var a = ie;
      ie |= 4, yo.current = null, Vd(e, n), ga(n, e), hd(Ni), Br = !!ji, Ni = ji = null, e.current = n, Wd(n), wc(), ie = a, de = o, st.transition = i;
    } else e.current = n;
    if (El && (El = !1, Kt = e, Cl = l), i = e.pendingLanes, i === 0 && (qt = null), Sc(n.stateNode), Ge(e, Ne()), t !== null) for (r = e.onRecoverableError, n = 0; n < t.length; n++) l = t[n], r(l.value, { componentStack: l.stack, digest: l.digest });
    if (Nl) throw Nl = !1, e = xo, xo = null, e;
    return (Cl & 1) !== 0 && e.tag !== 0 && An(), i = e.pendingLanes, (i & 1) !== 0 ? e === ko ? Nr++ : (Nr = 0, ko = e) : Nr = 0, Vt(), null;
  }
  function An() {
    if (Kt !== null) {
      var e = vu(Cl), t = st.transition, n = de;
      try {
        if (st.transition = null, de = 16 > e ? 16 : e, Kt === null) var r = !1;
        else {
          if (e = Kt, Kt = null, Cl = 0, (ie & 6) !== 0) throw Error(s(331));
          var l = ie;
          for (ie |= 4, A = e.current; A !== null; ) {
            var i = A, o = i.child;
            if ((A.flags & 16) !== 0) {
              var a = i.deletions;
              if (a !== null) {
                for (var d = 0; d < a.length; d++) {
                  var w = a[d];
                  for (A = w; A !== null; ) {
                    var z = A;
                    switch (z.tag) {
                      case 0:
                      case 11:
                      case 15:
                        Sr(8, z, i);
                    }
                    var L = z.child;
                    if (L !== null) L.return = z, A = L;
                    else for (; A !== null; ) {
                      z = A;
                      var j = z.sibling, U = z.return;
                      if (pa(z), z === w) {
                        A = null;
                        break;
                      }
                      if (j !== null) {
                        j.return = U, A = j;
                        break;
                      }
                      A = U;
                    }
                  }
                }
                var V = i.alternate;
                if (V !== null) {
                  var H = V.child;
                  if (H !== null) {
                    V.child = null;
                    do {
                      var Ee = H.sibling;
                      H.sibling = null, H = Ee;
                    } while (H !== null);
                  }
                }
                A = i;
              }
            }
            if ((i.subtreeFlags & 2064) !== 0 && o !== null) o.return = i, A = o;
            else e: for (; A !== null; ) {
              if (i = A, (i.flags & 2048) !== 0) switch (i.tag) {
                case 0:
                case 11:
                case 15:
                  Sr(9, i, i.return);
              }
              var v = i.sibling;
              if (v !== null) {
                v.return = i.return, A = v;
                break e;
              }
              A = i.return;
            }
          }
          var p = e.current;
          for (A = p; A !== null; ) {
            o = A;
            var g = o.child;
            if ((o.subtreeFlags & 2064) !== 0 && g !== null) g.return = o, A = g;
            else e: for (o = p; A !== null; ) {
              if (a = A, (a.flags & 2048) !== 0) try {
                switch (a.tag) {
                  case 0:
                  case 11:
                  case 15:
                    Sl(9, a);
                }
              } catch (Q) {
                je(a, a.return, Q);
              }
              if (a === o) {
                A = null;
                break e;
              }
              var T = a.sibling;
              if (T !== null) {
                T.return = a.return, A = T;
                break e;
              }
              A = a.return;
            }
          }
          if (ie = l, Vt(), wt && typeof wt.onPostCommitFiberRoot == "function") try {
            wt.onPostCommitFiberRoot(Dr, e);
          } catch {
          }
          r = !0;
        }
        return r;
      } finally {
        de = n, st.transition = t;
      }
    }
    return !1;
  }
  function Pa(e, t, n) {
    t = On(n, t), t = Ks(e, t, 1), e = Ht(e, t, 1), t = He(), e !== null && (Xn(e, 1, t), Ge(e, t));
  }
  function je(e, t, n) {
    if (e.tag === 3) Pa(e, e, n);
    else for (; t !== null; ) {
      if (t.tag === 3) {
        Pa(t, e, n);
        break;
      } else if (t.tag === 1) {
        var r = t.stateNode;
        if (typeof t.type.getDerivedStateFromError == "function" || typeof r.componentDidCatch == "function" && (qt === null || !qt.has(r))) {
          e = On(n, e), e = Ys(t, e, 1), t = Ht(t, e, 1), e = He(), t !== null && (Xn(t, 1, e), Ge(t, e));
          break;
        }
      }
      t = t.return;
    }
  }
  function Xd(e, t, n) {
    var r = e.pingCache;
    r !== null && r.delete(t), t = He(), e.pingedLanes |= e.suspendedLanes & n, Te === e && (De & n) === n && (Re === 4 || Re === 3 && (De & 130023424) === De && 500 > Ne() - wo ? cn(e, 0) : go |= n), Ge(e, t);
  }
  function Ra(e, t) {
    t === 0 && ((e.mode & 1) === 0 ? t = 1 : (t = Fr, Fr <<= 1, (Fr & 130023424) === 0 && (Fr = 4194304)));
    var n = He();
    e = zt(e, t), e !== null && (Xn(e, t, n), Ge(e, n));
  }
  function Gd(e) {
    var t = e.memoizedState, n = 0;
    t !== null && (n = t.retryLane), Ra(e, n);
  }
  function Jd(e, t) {
    var n = 0;
    switch (e.tag) {
      case 13:
        var r = e.stateNode, l = e.memoizedState;
        l !== null && (n = l.retryLane);
        break;
      case 19:
        r = e.stateNode;
        break;
      default:
        throw Error(s(314));
    }
    r !== null && r.delete(t), Ra(e, n);
  }
  var La;
  La = function(e, t, n) {
    if (e !== null) if (e.memoizedProps !== t.pendingProps || qe.current) Ye = !0;
    else {
      if ((e.lanes & n) === 0 && (t.flags & 128) === 0) return Ye = !1, $d(e, t, n);
      Ye = (e.flags & 131072) !== 0;
    }
    else Ye = !1, xe && (t.flags & 1048576) !== 0 && cs(t, il, t.index);
    switch (t.lanes = 0, t.tag) {
      case 2:
        var r = t.type;
        xl(e, t), e = t.pendingProps;
        var l = zn(t, $e.current);
        In(t, n), l = Xi(null, t, r, e, l, n);
        var i = Gi();
        return t.flags |= 1, typeof l == "object" && l !== null && typeof l.render == "function" && l.$$typeof === void 0 ? (t.tag = 1, t.memoizedState = null, t.updateQueue = null, Ke(r) ? (i = !0, nl(t)) : i = !1, t.memoizedState = l.state !== null && l.state !== void 0 ? l.state : null, Vi(t), l.updater = gl, t.stateNode = l, l._reactInternals = t, no(t, r, e, n), t = oo(null, t, r, !0, i, n)) : (t.tag = 0, xe && i && Ti(t), We(null, t, l, n), t = t.child), t;
      case 16:
        r = t.elementType;
        e: {
          switch (xl(e, t), e = t.pendingProps, l = r._init, r = l(r._payload), t.type = r, l = t.tag = bd(r), e = ht(r, e), l) {
            case 0:
              t = io(null, t, r, e, n);
              break e;
            case 1:
              t = ra(null, t, r, e, n);
              break e;
            case 11:
              t = Zs(null, t, r, e, n);
              break e;
            case 14:
              t = bs(null, t, r, ht(r.type, e), n);
              break e;
          }
          throw Error(s(
            306,
            r,
            ""
          ));
        }
        return t;
      case 0:
        return r = t.type, l = t.pendingProps, l = t.elementType === r ? l : ht(r, l), io(e, t, r, l, n);
      case 1:
        return r = t.type, l = t.pendingProps, l = t.elementType === r ? l : ht(r, l), ra(e, t, r, l, n);
      case 3:
        e: {
          if (la(t), e === null) throw Error(s(387));
          r = t.pendingProps, i = t.memoizedState, l = i.element, ws(e, t), dl(t, r, null, n);
          var o = t.memoizedState;
          if (r = o.element, i.isDehydrated) if (i = { element: r, isDehydrated: !1, cache: o.cache, pendingSuspenseBoundaries: o.pendingSuspenseBoundaries, transitions: o.transitions }, t.updateQueue.baseState = i, t.memoizedState = i, t.flags & 256) {
            l = On(Error(s(423)), t), t = ia(e, t, r, n, l);
            break e;
          } else if (r !== l) {
            l = On(Error(s(424)), t), t = ia(e, t, r, n, l);
            break e;
          } else for (tt = Ut(t.stateNode.containerInfo.firstChild), et = t, xe = !0, pt = null, n = ys(t, null, r, n), t.child = n; n; ) n.flags = n.flags & -3 | 4096, n = n.sibling;
          else {
            if (Ln(), r === l) {
              t = Rt(e, t, n);
              break e;
            }
            We(e, t, r, n);
          }
          t = t.child;
        }
        return t;
      case 5:
        return Ss(t), e === null && Di(t), r = t.type, l = t.pendingProps, i = e !== null ? e.memoizedProps : null, o = l.children, Ei(r, l) ? o = null : i !== null && Ei(r, i) && (t.flags |= 32), na(e, t), We(e, t, o, n), t.child;
      case 6:
        return e === null && Di(t), null;
      case 13:
        return oa(e, t, n);
      case 4:
        return Wi(t, t.stateNode.containerInfo), r = t.pendingProps, e === null ? t.child = Tn(t, null, r, n) : We(e, t, r, n), t.child;
      case 11:
        return r = t.type, l = t.pendingProps, l = t.elementType === r ? l : ht(r, l), Zs(e, t, r, l, n);
      case 7:
        return We(e, t, t.pendingProps, n), t.child;
      case 8:
        return We(e, t, t.pendingProps.children, n), t.child;
      case 12:
        return We(e, t, t.pendingProps.children, n), t.child;
      case 10:
        e: {
          if (r = t.type._context, l = t.pendingProps, i = t.memoizedProps, o = l.value, me(sl, r._currentValue), r._currentValue = o, i !== null) if (ft(i.value, o)) {
            if (i.children === l.children && !qe.current) {
              t = Rt(e, t, n);
              break e;
            }
          } else for (i = t.child, i !== null && (i.return = t); i !== null; ) {
            var a = i.dependencies;
            if (a !== null) {
              o = i.child;
              for (var d = a.firstContext; d !== null; ) {
                if (d.context === r) {
                  if (i.tag === 1) {
                    d = Pt(-1, n & -n), d.tag = 2;
                    var w = i.updateQueue;
                    if (w !== null) {
                      w = w.shared;
                      var z = w.pending;
                      z === null ? d.next = d : (d.next = z.next, z.next = d), w.pending = d;
                    }
                  }
                  i.lanes |= n, d = i.alternate, d !== null && (d.lanes |= n), Ai(
                    i.return,
                    n,
                    t
                  ), a.lanes |= n;
                  break;
                }
                d = d.next;
              }
            } else if (i.tag === 10) o = i.type === t.type ? null : i.child;
            else if (i.tag === 18) {
              if (o = i.return, o === null) throw Error(s(341));
              o.lanes |= n, a = o.alternate, a !== null && (a.lanes |= n), Ai(o, n, t), o = i.sibling;
            } else o = i.child;
            if (o !== null) o.return = i;
            else for (o = i; o !== null; ) {
              if (o === t) {
                o = null;
                break;
              }
              if (i = o.sibling, i !== null) {
                i.return = o.return, o = i;
                break;
              }
              o = o.return;
            }
            i = o;
          }
          We(e, t, l.children, n), t = t.child;
        }
        return t;
      case 9:
        return l = t.type, r = t.pendingProps.children, In(t, n), l = ot(l), r = r(l), t.flags |= 1, We(e, t, r, n), t.child;
      case 14:
        return r = t.type, l = ht(r, t.pendingProps), l = ht(r.type, l), bs(e, t, r, l, n);
      case 15:
        return ea(e, t, t.type, t.pendingProps, n);
      case 17:
        return r = t.type, l = t.pendingProps, l = t.elementType === r ? l : ht(r, l), xl(e, t), t.tag = 1, Ke(r) ? (e = !0, nl(t)) : e = !1, In(t, n), Qs(t, r, l), no(t, r, l, n), oo(null, t, r, !0, e, n);
      case 19:
        return sa(e, t, n);
      case 22:
        return ta(e, t, n);
    }
    throw Error(s(156, t.tag));
  };
  function Ta(e, t) {
    return du(e, t);
  }
  function Zd(e, t, n, r) {
    this.tag = e, this.key = n, this.sibling = this.child = this.return = this.stateNode = this.type = this.elementType = null, this.index = 0, this.ref = null, this.pendingProps = t, this.dependencies = this.memoizedState = this.updateQueue = this.memoizedProps = null, this.mode = r, this.subtreeFlags = this.flags = 0, this.deletions = null, this.childLanes = this.lanes = 0, this.alternate = null;
  }
  function at(e, t, n, r) {
    return new Zd(e, t, n, r);
  }
  function Co(e) {
    return e = e.prototype, !(!e || !e.isReactComponent);
  }
  function bd(e) {
    if (typeof e == "function") return Co(e) ? 1 : 0;
    if (e != null) {
      if (e = e.$$typeof, e === rt) return 11;
      if (e === gt) return 14;
    }
    return 2;
  }
  function Gt(e, t) {
    var n = e.alternate;
    return n === null ? (n = at(e.tag, t, e.key, e.mode), n.elementType = e.elementType, n.type = e.type, n.stateNode = e.stateNode, n.alternate = e, e.alternate = n) : (n.pendingProps = t, n.type = e.type, n.flags = 0, n.subtreeFlags = 0, n.deletions = null), n.flags = e.flags & 14680064, n.childLanes = e.childLanes, n.lanes = e.lanes, n.child = e.child, n.memoizedProps = e.memoizedProps, n.memoizedState = e.memoizedState, n.updateQueue = e.updateQueue, t = e.dependencies, n.dependencies = t === null ? null : { lanes: t.lanes, firstContext: t.firstContext }, n.sibling = e.sibling, n.index = e.index, n.ref = e.ref, n;
  }
  function Ll(e, t, n, r, l, i) {
    var o = 2;
    if (r = e, typeof e == "function") Co(e) && (o = 1);
    else if (typeof e == "string") o = 5;
    else e: switch (e) {
      case q:
        return fn(n.children, l, i, t);
      case D:
        o = 8, l |= 8;
        break;
      case pe:
        return e = at(12, n, t, l | 2), e.elementType = pe, e.lanes = i, e;
      case Fe:
        return e = at(13, n, t, l), e.elementType = Fe, e.lanes = i, e;
      case ct:
        return e = at(19, n, t, l), e.elementType = ct, e.lanes = i, e;
      case _e:
        return Tl(n, l, i, t);
      default:
        if (typeof e == "object" && e !== null) switch (e.$$typeof) {
          case ae:
            o = 10;
            break e;
          case Oe:
            o = 9;
            break e;
          case rt:
            o = 11;
            break e;
          case gt:
            o = 14;
            break e;
          case Qe:
            o = 16, r = null;
            break e;
        }
        throw Error(s(130, e == null ? e : typeof e, ""));
    }
    return t = at(o, n, t, l), t.elementType = e, t.type = r, t.lanes = i, t;
  }
  function fn(e, t, n, r) {
    return e = at(7, e, r, t), e.lanes = n, e;
  }
  function Tl(e, t, n, r) {
    return e = at(22, e, r, t), e.elementType = _e, e.lanes = n, e.stateNode = { isHidden: !1 }, e;
  }
  function zo(e, t, n) {
    return e = at(6, e, null, t), e.lanes = n, e;
  }
  function Po(e, t, n) {
    return t = at(4, e.children !== null ? e.children : [], e.key, t), t.lanes = n, t.stateNode = { containerInfo: e.containerInfo, pendingChildren: null, implementation: e.implementation }, t;
  }
  function ef(e, t, n, r, l) {
    this.tag = t, this.containerInfo = e, this.finishedWork = this.pingCache = this.current = this.pendingChildren = null, this.timeoutHandle = -1, this.callbackNode = this.pendingContext = this.context = null, this.callbackPriority = 0, this.eventTimes = ti(0), this.expirationTimes = ti(-1), this.entangledLanes = this.finishedLanes = this.mutableReadLanes = this.expiredLanes = this.pingedLanes = this.suspendedLanes = this.pendingLanes = 0, this.entanglements = ti(0), this.identifierPrefix = r, this.onRecoverableError = l, this.mutableSourceEagerHydrationData = null;
  }
  function Ro(e, t, n, r, l, i, o, a, d) {
    return e = new ef(e, t, n, a, d), t === 1 ? (t = 1, i === !0 && (t |= 8)) : t = 0, i = at(3, null, null, t), e.current = i, i.stateNode = e, i.memoizedState = { element: r, isDehydrated: n, cache: null, transitions: null, pendingSuspenseBoundaries: null }, Vi(i), e;
  }
  function tf(e, t, n) {
    var r = 3 < arguments.length && arguments[3] !== void 0 ? arguments[3] : null;
    return { $$typeof: ge, key: r == null ? null : "" + r, children: e, containerInfo: t, implementation: n };
  }
  function Ma(e) {
    if (!e) return Bt;
    e = e._reactInternals;
    e: {
      if (bt(e) !== e || e.tag !== 1) throw Error(s(170));
      var t = e;
      do {
        switch (t.tag) {
          case 3:
            t = t.stateNode.context;
            break e;
          case 1:
            if (Ke(t.type)) {
              t = t.stateNode.__reactInternalMemoizedMergedChildContext;
              break e;
            }
        }
        t = t.return;
      } while (t !== null);
      throw Error(s(171));
    }
    if (e.tag === 1) {
      var n = e.type;
      if (Ke(n)) return us(e, n, t);
    }
    return t;
  }
  function Ia(e, t, n, r, l, i, o, a, d) {
    return e = Ro(n, r, !0, e, l, i, o, a, d), e.context = Ma(null), n = e.current, r = He(), l = Yt(n), i = Pt(r, l), i.callback = t ?? null, Ht(n, i, l), e.current.lanes = l, Xn(e, l, r), Ge(e, r), e;
  }
  function Ml(e, t, n, r) {
    var l = t.current, i = He(), o = Yt(l);
    return n = Ma(n), t.context === null ? t.context = n : t.pendingContext = n, t = Pt(i, o), t.payload = { element: e }, r = r === void 0 ? null : r, r !== null && (t.callback = r), e = Ht(l, t, o), e !== null && (yt(e, l, o, i), cl(e, l, o)), o;
  }
  function Il(e) {
    if (e = e.current, !e.child) return null;
    switch (e.child.tag) {
      case 5:
        return e.child.stateNode;
      default:
        return e.child.stateNode;
    }
  }
  function Da(e, t) {
    if (e = e.memoizedState, e !== null && e.dehydrated !== null) {
      var n = e.retryLane;
      e.retryLane = n !== 0 && n < t ? n : t;
    }
  }
  function Lo(e, t) {
    Da(e, t), (e = e.alternate) && Da(e, t);
  }
  function nf() {
    return null;
  }
  var Oa = typeof reportError == "function" ? reportError : function(e) {
    console.error(e);
  };
  function To(e) {
    this._internalRoot = e;
  }
  Dl.prototype.render = To.prototype.render = function(e) {
    var t = this._internalRoot;
    if (t === null) throw Error(s(409));
    Ml(e, t, null, null);
  }, Dl.prototype.unmount = To.prototype.unmount = function() {
    var e = this._internalRoot;
    if (e !== null) {
      this._internalRoot = null;
      var t = e.containerInfo;
      an(function() {
        Ml(null, e, null, null);
      }), t[jt] = null;
    }
  };
  function Dl(e) {
    this._internalRoot = e;
  }
  Dl.prototype.unstable_scheduleHydration = function(e) {
    if (e) {
      var t = wu();
      e = { blockedOn: null, target: e, priority: t };
      for (var n = 0; n < Ot.length && t !== 0 && t < Ot[n].priority; n++) ;
      Ot.splice(n, 0, e), n === 0 && Su(e);
    }
  };
  function Mo(e) {
    return !(!e || e.nodeType !== 1 && e.nodeType !== 9 && e.nodeType !== 11);
  }
  function Ol(e) {
    return !(!e || e.nodeType !== 1 && e.nodeType !== 9 && e.nodeType !== 11 && (e.nodeType !== 8 || e.nodeValue !== " react-mount-point-unstable "));
  }
  function Fa() {
  }
  function rf(e, t, n, r, l) {
    if (l) {
      if (typeof r == "function") {
        var i = r;
        r = function() {
          var w = Il(o);
          i.call(w);
        };
      }
      var o = Ia(t, r, e, 0, null, !1, !1, "", Fa);
      return e._reactRootContainer = o, e[jt] = o.current, ar(e.nodeType === 8 ? e.parentNode : e), an(), o;
    }
    for (; l = e.lastChild; ) e.removeChild(l);
    if (typeof r == "function") {
      var a = r;
      r = function() {
        var w = Il(d);
        a.call(w);
      };
    }
    var d = Ro(e, 0, !1, null, null, !1, !1, "", Fa);
    return e._reactRootContainer = d, e[jt] = d.current, ar(e.nodeType === 8 ? e.parentNode : e), an(function() {
      Ml(t, d, n, r);
    }), d;
  }
  function Fl(e, t, n, r, l) {
    var i = n._reactRootContainer;
    if (i) {
      var o = i;
      if (typeof l == "function") {
        var a = l;
        l = function() {
          var d = Il(o);
          a.call(d);
        };
      }
      Ml(t, o, e, l);
    } else o = rf(n, t, e, l, r);
    return Il(o);
  }
  yu = function(e) {
    switch (e.tag) {
      case 3:
        var t = e.stateNode;
        if (t.current.memoizedState.isDehydrated) {
          var n = Yn(t.pendingLanes);
          n !== 0 && (ni(t, n | 1), Ge(t, Ne()), (ie & 6) === 0 && (Un = Ne() + 500, Vt()));
        }
        break;
      case 13:
        an(function() {
          var r = zt(e, 1);
          if (r !== null) {
            var l = He();
            yt(r, e, 1, l);
          }
        }), Lo(e, 1);
    }
  }, ri = function(e) {
    if (e.tag === 13) {
      var t = zt(e, 134217728);
      if (t !== null) {
        var n = He();
        yt(t, e, 134217728, n);
      }
      Lo(e, 134217728);
    }
  }, gu = function(e) {
    if (e.tag === 13) {
      var t = Yt(e), n = zt(e, t);
      if (n !== null) {
        var r = He();
        yt(n, e, t, r);
      }
      Lo(e, t);
    }
  }, wu = function() {
    return de;
  }, xu = function(e, t) {
    var n = de;
    try {
      return de = e, t();
    } finally {
      de = n;
    }
  }, Xl = function(e, t, n) {
    switch (t) {
      case "input":
        if (Bl(e, n), t = n.name, n.type === "radio" && t != null) {
          for (n = e; n.parentNode; ) n = n.parentNode;
          for (n = n.querySelectorAll("input[name=" + JSON.stringify("" + t) + '][type="radio"]'), t = 0; t < n.length; t++) {
            var r = n[t];
            if (r !== e && r.form === e.form) {
              var l = el(r);
              if (!l) throw Error(s(90));
              Ho(r), Bl(r, l);
            }
          }
        }
        break;
      case "textarea":
        Xo(e, n);
        break;
      case "select":
        t = n.value, t != null && vn(e, !!n.multiple, t, !1);
    }
  }, lu = jo, iu = an;
  var lf = { usingClientEntryPoint: !1, Events: [fr, En, el, nu, ru, jo] }, Er = { findFiberByHostInstance: en, bundleType: 0, version: "18.3.1", rendererPackageName: "react-dom" }, of = { bundleType: Er.bundleType, version: Er.version, rendererPackageName: Er.rendererPackageName, rendererConfig: Er.rendererConfig, overrideHookState: null, overrideHookStateDeletePath: null, overrideHookStateRenamePath: null, overrideProps: null, overridePropsDeletePath: null, overridePropsRenamePath: null, setErrorHandler: null, setSuspenseHandler: null, scheduleUpdate: null, currentDispatcherRef: ee.ReactCurrentDispatcher, findHostInstanceByFiber: function(e) {
    return e = au(e), e === null ? null : e.stateNode;
  }, findFiberByHostInstance: Er.findFiberByHostInstance || nf, findHostInstancesForRefresh: null, scheduleRefresh: null, scheduleRoot: null, setRefreshHandler: null, getCurrentFiber: null, reconcilerVersion: "18.3.1-next-f1338f8080-20240426" };
  if (typeof __REACT_DEVTOOLS_GLOBAL_HOOK__ < "u") {
    var $l = __REACT_DEVTOOLS_GLOBAL_HOOK__;
    if (!$l.isDisabled && $l.supportsFiber) try {
      Dr = $l.inject(of), wt = $l;
    } catch {
    }
  }
  return Je.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED = lf, Je.createPortal = function(e, t) {
    var n = 2 < arguments.length && arguments[2] !== void 0 ? arguments[2] : null;
    if (!Mo(t)) throw Error(s(200));
    return tf(e, t, null, n);
  }, Je.createRoot = function(e, t) {
    if (!Mo(e)) throw Error(s(299));
    var n = !1, r = "", l = Oa;
    return t != null && (t.unstable_strictMode === !0 && (n = !0), t.identifierPrefix !== void 0 && (r = t.identifierPrefix), t.onRecoverableError !== void 0 && (l = t.onRecoverableError)), t = Ro(e, 1, !1, null, null, n, !1, r, l), e[jt] = t.current, ar(e.nodeType === 8 ? e.parentNode : e), new To(t);
  }, Je.findDOMNode = function(e) {
    if (e == null) return null;
    if (e.nodeType === 1) return e;
    var t = e._reactInternals;
    if (t === void 0)
      throw typeof e.render == "function" ? Error(s(188)) : (e = Object.keys(e).join(","), Error(s(268, e)));
    return e = au(t), e = e === null ? null : e.stateNode, e;
  }, Je.flushSync = function(e) {
    return an(e);
  }, Je.hydrate = function(e, t, n) {
    if (!Ol(t)) throw Error(s(200));
    return Fl(null, e, t, !0, n);
  }, Je.hydrateRoot = function(e, t, n) {
    if (!Mo(e)) throw Error(s(405));
    var r = n != null && n.hydratedSources || null, l = !1, i = "", o = Oa;
    if (n != null && (n.unstable_strictMode === !0 && (l = !0), n.identifierPrefix !== void 0 && (i = n.identifierPrefix), n.onRecoverableError !== void 0 && (o = n.onRecoverableError)), t = Ia(t, null, e, 1, n ?? null, l, !1, i, o), e[jt] = t.current, ar(e), r) for (e = 0; e < r.length; e++) n = r[e], l = n._getVersion, l = l(n._source), t.mutableSourceEagerHydrationData == null ? t.mutableSourceEagerHydrationData = [n, l] : t.mutableSourceEagerHydrationData.push(
      n,
      l
    );
    return new Dl(t);
  }, Je.render = function(e, t, n) {
    if (!Ol(t)) throw Error(s(200));
    return Fl(null, e, t, !1, n);
  }, Je.unmountComponentAtNode = function(e) {
    if (!Ol(e)) throw Error(s(40));
    return e._reactRootContainer ? (an(function() {
      Fl(null, null, e, !1, function() {
        e._reactRootContainer = null, e[jt] = null;
      });
    }), !0) : !1;
  }, Je.unstable_batchedUpdates = jo, Je.unstable_renderSubtreeIntoContainer = function(e, t, n, r) {
    if (!Ol(n)) throw Error(s(200));
    if (e == null || e._reactInternals === void 0) throw Error(s(38));
    return Fl(e, t, n, !1, r);
  }, Je.version = "18.3.1-next-f1338f8080-20240426", Je;
}
var Qa;
function ba() {
  if (Qa) return Oo.exports;
  Qa = 1;
  function c() {
    if (!(typeof __REACT_DEVTOOLS_GLOBAL_HOOK__ > "u" || typeof __REACT_DEVTOOLS_GLOBAL_HOOK__.checkDCE != "function"))
      try {
        __REACT_DEVTOOLS_GLOBAL_HOOK__.checkDCE(c);
      } catch (f) {
        console.error(f);
      }
  }
  return c(), Oo.exports = pf(), Oo.exports;
}
var qa;
function hf() {
  if (qa) return Ul;
  qa = 1;
  var c = ba();
  return Ul.createRoot = c.createRoot, Ul.hydrateRoot = c.hydrateRoot, Ul;
}
var mf = hf(), R = Bo(), vf = ba();
function le(...c) {
  let f = "";
  for (const s of c)
    s && (f = f ? `${f} ${s}` : `${s}`);
  return f;
}
const yf = {
  0: "0",
  4: "var(--s-1)",
  8: "var(--s-2)",
  12: "var(--s-3)",
  16: "var(--s-4)",
  24: "var(--s-5)",
  32: "var(--s-6)",
  48: "var(--s-7)",
  64: "var(--s-8)"
};
function Bn(c) {
  return yf[c] ?? "0";
}
const Vo = R.forwardRef(function({ size: c = 16, strokeWidth: f, color: s, className: y, style: m, ...x }, _) {
  const k = f ?? Math.max(1.5, c * 0.14);
  return /* @__PURE__ */ u.jsx(
    "svg",
    {
      ref: _,
      className: le("nc-chevron", y),
      width: c,
      height: c,
      viewBox: "0 0 16 16",
      fill: "none",
      role: "img",
      "aria-label": "chevron",
      style: { color: s ?? void 0, ...m },
      ...x,
      children: /* @__PURE__ */ u.jsx(
        "path",
        {
          d: "M5 3 L11 8 L5 13",
          stroke: "currentColor",
          strokeWidth: k / c * 16,
          strokeLinecap: "round",
          strokeLinejoin: "round"
        }
      )
    }
  );
});
R.forwardRef(function({ height: c = 40, showCursor: f = !0, filled: s = !1, className: y, ...m }, x) {
  const _ = c / 160 * 300;
  return /* @__PURE__ */ u.jsxs(
    "svg",
    {
      ref: x,
      className: le("nc-lockup", y),
      width: _,
      height: c,
      viewBox: "0 0 300 160",
      role: "img",
      "aria-label": "nalet",
      ...m,
      children: [
        s ? /* @__PURE__ */ u.jsx("rect", { width: "300", height: "160", fill: "var(--bg)" }) : null,
        /* @__PURE__ */ u.jsx(
          "path",
          {
            d: "M46 50 L76 80 L46 110",
            fill: "none",
            stroke: "var(--cloud-blue)",
            strokeWidth: "9",
            strokeLinecap: "round",
            strokeLinejoin: "round"
          }
        ),
        /* @__PURE__ */ u.jsx(
          "text",
          {
            x: "100",
            y: "100",
            fontFamily: "var(--ff-mono)",
            fontWeight: "700",
            fontSize: "44",
            fill: "var(--fg-2)",
            letterSpacing: "0.5",
            children: "nalet"
          }
        ),
        f ? /* @__PURE__ */ u.jsx("rect", { className: "nc-lockup__cursor", x: "240", y: "58", width: "14", height: "48", fill: "var(--cloud-blue)", children: /* @__PURE__ */ u.jsx(
          "animate",
          {
            attributeName: "opacity",
            values: "1;1;0;0",
            keyTimes: "0;0.5;0.5;1",
            dur: "1.05s",
            repeatCount: "indefinite"
          }
        ) }) : null
      ]
    }
  );
});
R.forwardRef(function({ icon: c, size: f = 16, strokeWidth: s = 1.75, className: y, ...m }, x) {
  return /* @__PURE__ */ u.jsx(
    c,
    {
      ref: x,
      className: le("nc-icon", y),
      size: f,
      strokeWidth: s,
      "aria-hidden": m["aria-label"] ? void 0 : !0,
      ...m
    }
  );
});
const gf = {
  bg: "var(--bg)",
  "bg-2": "var(--bg-2)",
  surface: "var(--surface)",
  "surface-2": "var(--surface-2)"
};
R.forwardRef(function({ p: c, px: f, py: s, bg: y, border: m, className: x, style: _, ...k }, M) {
  const I = { ..._ };
  return c !== void 0 && (I.padding = Bn(c)), f !== void 0 && (I.paddingLeft = Bn(f), I.paddingRight = Bn(f)), s !== void 0 && (I.paddingTop = Bn(s), I.paddingBottom = Bn(s)), y && (I.background = gf[y]), /* @__PURE__ */ u.jsx(
    "div",
    {
      ref: M,
      className: le("nc-box", m && "nc-box--border", x),
      style: I,
      ...k
    }
  );
});
const wf = {
  start: "flex-start",
  center: "center",
  end: "flex-end",
  stretch: "stretch",
  baseline: "baseline"
}, xf = {
  start: "flex-start",
  center: "center",
  end: "flex-end",
  between: "space-between",
  around: "space-around"
};
R.forwardRef(function({ direction: c = "vertical", gap: f = 16, align: s, justify: y, wrap: m, inline: x, className: _, style: k, ...M }, I) {
  const W = {
    display: x ? "inline-flex" : "flex",
    flexDirection: c === "horizontal" ? "row" : "column",
    gap: Bn(f),
    ...k
  };
  return s && (W.alignItems = wf[s]), y && (W.justifyContent = xf[y]), m && (W.flexWrap = "wrap"), /* @__PURE__ */ u.jsx("div", { ref: I, className: le("nc-stack", _), style: W, ...M });
});
R.forwardRef(function({ orientation: c = "horizontal", heavy: f, label: s, className: y, ...m }, x) {
  const _ = c === "vertical";
  return s && !_ ? /* @__PURE__ */ u.jsxs(
    "div",
    {
      ref: x,
      className: le("nc-divider", "nc-divider--labeled", f && "nc-divider--heavy", y),
      role: "separator",
      ...m,
      children: [
        /* @__PURE__ */ u.jsx("span", { className: "nc-divider__line" }),
        /* @__PURE__ */ u.jsx("span", { className: "nc-divider__label", children: s }),
        /* @__PURE__ */ u.jsx("span", { className: "nc-divider__line" })
      ]
    }
  ) : /* @__PURE__ */ u.jsx(
    "div",
    {
      ref: x,
      className: le(
        "nc-divider",
        _ ? "nc-divider--vertical" : "nc-divider--horizontal",
        f && "nc-divider--heavy",
        y
      ),
      role: "separator",
      "aria-orientation": c,
      ...m
    }
  );
});
const Ce = R.forwardRef(function({ variant: c = "body", as: f = "span", truncate: s, className: y, ...m }, x) {
  return /* @__PURE__ */ u.jsx(
    f,
    {
      ref: x,
      className: le("nc-text", `nc-text--${c}`, s && "nc-text--truncate", y),
      ...m
    }
  );
});
R.forwardRef(function({ level: c = 1, chevron: f, className: s, children: y, ...m }, x) {
  return /* @__PURE__ */ u.jsxs(c === 1 ? "h1" : "h2", { ref: x, className: le("nc-heading", `nc-heading--h${c}`, s), ...m, children: [
    f ? /* @__PURE__ */ u.jsx("span", { className: "nc-heading__chevron", "aria-hidden": "true", children: ">" }) : null,
    y
  ] });
});
R.forwardRef(function({ className: c, ...f }, s) {
  return /* @__PURE__ */ u.jsx("kbd", { ref: s, className: le("nc-kbd", c), ...f });
});
R.forwardRef(function({ block: c, className: f, children: s, ...y }, m) {
  return c ? /* @__PURE__ */ u.jsx(
    "pre",
    {
      ref: m,
      className: le("nc-code", "nc-code--block", f),
      ...y,
      children: /* @__PURE__ */ u.jsx("code", { children: s })
    }
  ) : /* @__PURE__ */ u.jsx("code", { ref: m, className: le("nc-code", "nc-code--inline", f), ...y, children: s });
});
const ec = R.forwardRef(function({ size: c = 16, color: f, label: s = "loading", className: y, style: m, ...x }, _) {
  return /* @__PURE__ */ u.jsx(
    "span",
    {
      ref: _,
      className: le("nc-spinner", y),
      role: "status",
      "aria-label": s,
      style: { width: c, height: c, color: f, ...m },
      ...x,
      children: /* @__PURE__ */ u.jsxs("svg", { viewBox: "0 0 16 16", width: c, height: c, fill: "none", "aria-hidden": "true", children: [
        /* @__PURE__ */ u.jsx("circle", { cx: "8", cy: "8", r: "6.5", stroke: "currentColor", strokeOpacity: "0.2", strokeWidth: "2" }),
        /* @__PURE__ */ u.jsx(
          "path",
          {
            className: "nc-spinner__arc",
            d: "M8 1.5 A6.5 6.5 0 0 1 14.5 8",
            stroke: "currentColor",
            strokeWidth: "2",
            strokeLinecap: "butt"
          }
        )
      ] })
    }
  );
}), Ve = R.forwardRef(function({
  variant: c = "default",
  size: f = "md",
  loading: s = !1,
  block: y = !1,
  leading: m,
  trailing: x,
  disabled: _,
  className: k,
  children: M,
  type: I = "button",
  ...W
}, P) {
  return /* @__PURE__ */ u.jsxs(
    "button",
    {
      ref: P,
      type: I,
      className: le(
        "nc-btn",
        `nc-btn--${c}`,
        `nc-btn--${f}`,
        y && "nc-btn--block",
        s && "nc-btn--loading",
        k
      ),
      disabled: _ || s,
      "aria-busy": s || void 0,
      ...W,
      children: [
        s ? /* @__PURE__ */ u.jsx("span", { className: "nc-btn__slot", children: /* @__PURE__ */ u.jsx(ec, { size: f === "sm" ? 12 : 14 }) }) : m ? /* @__PURE__ */ u.jsx("span", { className: "nc-btn__slot", children: m }) : null,
        /* @__PURE__ */ u.jsx("span", { className: "nc-btn__label", children: M }),
        x && !s ? /* @__PURE__ */ u.jsx("span", { className: "nc-btn__slot", children: x }) : null
      ]
    }
  );
});
R.forwardRef(function({ label: c, variant: f = "default", size: s = "md", loading: y = !1, disabled: m, className: x, children: _, type: k = "button", ...M }, I) {
  return /* @__PURE__ */ u.jsx(
    "button",
    {
      ref: I,
      type: k,
      "aria-label": c,
      title: c,
      className: le("nc-iconbtn", `nc-iconbtn--${f}`, `nc-iconbtn--${s}`, x),
      disabled: m || y,
      "aria-busy": y || void 0,
      ...M,
      children: y ? /* @__PURE__ */ u.jsx(ec, { size: s === "sm" ? 12 : 14 }) : _
    }
  );
});
const hn = R.forwardRef(function({ inputSize: c = "md", invalid: f, leading: s, trailing: y, disabled: m, className: x, ..._ }, k) {
  return /* @__PURE__ */ u.jsxs(
    "div",
    {
      className: le(
        "nc-input",
        `nc-input--${c}`,
        f && "nc-input--invalid",
        m && "nc-input--disabled",
        x
      ),
      children: [
        s ? /* @__PURE__ */ u.jsx("span", { className: "nc-input__affix nc-input__affix--prefix", children: s }) : null,
        /* @__PURE__ */ u.jsx(
          "input",
          {
            ref: k,
            className: "nc-input__control",
            disabled: m,
            "aria-invalid": f || void 0,
            ..._
          }
        ),
        y ? /* @__PURE__ */ u.jsx("span", { className: "nc-input__affix nc-input__affix--suffix", children: y }) : null
      ]
    }
  );
});
R.forwardRef(function({ invalid: c, className: f, rows: s = 4, ...y }, m) {
  return /* @__PURE__ */ u.jsx(
    "textarea",
    {
      ref: m,
      rows: s,
      className: le("nc-textarea", c && "nc-textarea--invalid", f),
      "aria-invalid": c || void 0,
      ...y
    }
  );
});
const kf = R.forwardRef(function({ selectSize: c = "md", invalid: f, options: s, disabled: y, className: m, children: x, ..._ }, k) {
  return /* @__PURE__ */ u.jsxs(
    "div",
    {
      className: le(
        "nc-select",
        `nc-select--${c}`,
        f && "nc-select--invalid",
        y && "nc-select--disabled",
        m
      ),
      children: [
        /* @__PURE__ */ u.jsx(
          "select",
          {
            ref: k,
            className: "nc-select__control",
            disabled: y,
            "aria-invalid": f || void 0,
            ..._,
            children: s ? s.map((M) => /* @__PURE__ */ u.jsx("option", { value: M.value, disabled: M.disabled, children: M.label }, M.value)) : x
          }
        ),
        /* @__PURE__ */ u.jsx("span", { className: "nc-select__chevron", "aria-hidden": "true", children: /* @__PURE__ */ u.jsx("svg", { viewBox: "0 0 12 12", width: "12", height: "12", fill: "none", children: /* @__PURE__ */ u.jsx("path", { d: "M3 4.5 L6 7.5 L9 4.5", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round", strokeLinejoin: "round" }) }) })
      ]
    }
  );
}), Ao = R.forwardRef(function({ label: c, indeterminate: f = !1, disabled: s, className: y, id: m, ...x }, _) {
  const k = R.useRef(null);
  return R.useImperativeHandle(_, () => k.current), R.useEffect(() => {
    k.current && (k.current.indeterminate = f);
  }, [f]), /* @__PURE__ */ u.jsxs("label", { className: le("nc-checkbox", s && "nc-checkbox--disabled", y), children: [
    /* @__PURE__ */ u.jsxs("span", { className: "nc-checkbox__box", children: [
      /* @__PURE__ */ u.jsx(
        "input",
        {
          ref: k,
          id: m,
          type: "checkbox",
          className: "nc-checkbox__input",
          disabled: s,
          ...x
        }
      ),
      /* @__PURE__ */ u.jsx("span", { className: "nc-checkbox__mark", "aria-hidden": "true", children: f ? /* @__PURE__ */ u.jsx("svg", { viewBox: "0 0 12 12", width: "12", height: "12", fill: "none", children: /* @__PURE__ */ u.jsx("path", { d: "M2.5 6 H9.5", stroke: "currentColor", strokeWidth: "1.75", strokeLinecap: "round" }) }) : /* @__PURE__ */ u.jsx("svg", { viewBox: "0 0 12 12", width: "12", height: "12", fill: "none", children: /* @__PURE__ */ u.jsx("path", { d: "M2.5 6.2 L5 8.7 L9.5 3.5", stroke: "currentColor", strokeWidth: "1.75", strokeLinecap: "round", strokeLinejoin: "round" }) }) })
    ] }),
    c != null ? /* @__PURE__ */ u.jsx("span", { className: "nc-checkbox__label", children: c }) : null
  ] });
});
R.forwardRef(function({ label: c, size: f = "md", disabled: s, className: y, id: m, ...x }, _) {
  return /* @__PURE__ */ u.jsxs("label", { className: le("nc-switch", `nc-switch--${f}`, s && "nc-switch--disabled", y), children: [
    /* @__PURE__ */ u.jsxs("span", { className: "nc-switch__track", children: [
      /* @__PURE__ */ u.jsx(
        "input",
        {
          ref: _,
          id: m,
          type: "checkbox",
          role: "switch",
          className: "nc-switch__input",
          disabled: s,
          ...x
        }
      ),
      /* @__PURE__ */ u.jsx("span", { className: "nc-switch__thumb", "aria-hidden": "true" })
    ] }),
    c != null ? /* @__PURE__ */ u.jsx("span", { className: "nc-switch__label", children: c }) : null
  ] });
});
const pn = R.forwardRef(function({ label: c, hint: f, error: s, required: y, children: m, htmlFor: x, className: _, ...k }, M) {
  const I = R.useId(), W = x ?? m.props.id ?? `nc-field-${I}`, P = f || s ? `${W}-desc` : void 0, S = R.cloneElement(m, {
    id: W,
    "aria-describedby": le(m.props["aria-describedby"], P) || void 0,
    "aria-invalid": s ? !0 : m.props["aria-invalid"],
    invalid: s ? !0 : m.props.invalid
  });
  return /* @__PURE__ */ u.jsxs("div", { ref: M, className: le("nc-field", _), ...k, children: [
    c != null ? /* @__PURE__ */ u.jsxs("label", { className: "nc-field__label", htmlFor: W, children: [
      c,
      y ? /* @__PURE__ */ u.jsx("span", { className: "nc-field__req", "aria-hidden": "true", children: " *" }) : null
    ] }) : null,
    S,
    s ? /* @__PURE__ */ u.jsx("span", { id: P, className: "nc-field__msg nc-field__msg--error", children: s }) : f ? /* @__PURE__ */ u.jsx("span", { id: P, className: "nc-field__msg nc-field__msg--hint", children: f }) : null
  ] });
});
R.forwardRef(function({ header: c, headerAside: f, footer: s, flush: y, sunken: m, className: x, children: _, ...k }, M) {
  return /* @__PURE__ */ u.jsxs(
    "div",
    {
      ref: M,
      className: le("nc-card", m && "nc-card--sunken", x),
      ...k,
      children: [
        c != null || f != null ? /* @__PURE__ */ u.jsxs("div", { className: "nc-card__header", children: [
          /* @__PURE__ */ u.jsx("div", { className: "nc-card__title", children: c }),
          f != null ? /* @__PURE__ */ u.jsx("div", { className: "nc-card__aside", children: f }) : null
        ] }) : null,
        /* @__PURE__ */ u.jsx("div", { className: le("nc-card__body", y && "nc-card__body--flush"), children: _ }),
        s != null ? /* @__PURE__ */ u.jsx("div", { className: "nc-card__footer", children: s }) : null
      ]
    }
  );
});
const Tt = R.forwardRef(function({ tone: c = "neutral", dot: f, className: s, children: y, ...m }, x) {
  return /* @__PURE__ */ u.jsxs("span", { ref: x, className: le("nc-badge", `nc-badge--${c}`, s), ...m, children: [
    f ? /* @__PURE__ */ u.jsx("span", { className: "nc-badge__dot", "aria-hidden": "true" }) : null,
    y
  ] });
});
function zr({
  columns: c,
  rows: f,
  rowKey: s,
  dense: y,
  empty: m = "no rows",
  className: x,
  ..._
}) {
  return /* @__PURE__ */ u.jsx("div", { className: "nc-table__scroll", children: /* @__PURE__ */ u.jsxs("table", { className: le("nc-table", y && "nc-table--dense", x), ..._, children: [
    /* @__PURE__ */ u.jsx("thead", { className: "nc-table__head", children: /* @__PURE__ */ u.jsx("tr", { children: c.map((k) => /* @__PURE__ */ u.jsx(
      "th",
      {
        className: "nc-table__th",
        style: { textAlign: k.align ?? "left", width: k.width },
        scope: "col",
        children: k.header
      },
      k.key
    )) }) }),
    /* @__PURE__ */ u.jsx("tbody", { children: f.length === 0 ? /* @__PURE__ */ u.jsx("tr", { children: /* @__PURE__ */ u.jsx("td", { className: "nc-table__empty", colSpan: c.length, children: m }) }) : f.map((k, M) => /* @__PURE__ */ u.jsx("tr", { className: "nc-table__tr", children: c.map((I) => /* @__PURE__ */ u.jsx("td", { className: "nc-table__td", style: { textAlign: I.align ?? "left" }, children: I.render ? I.render(k, M) : k[I.key] }, I.key)) }, s ? s(k, M) : M)) })
  ] }) });
}
const Sf = R.forwardRef(function({ items: c, value: f, defaultValue: s, onChange: y, className: m, ...x }, _) {
  var S;
  const [k, M] = R.useState(
    s ?? ((S = c[0]) == null ? void 0 : S.value) ?? ""
  ), I = f ?? k, W = (O) => {
    f === void 0 && M(O), y == null || y(O);
  }, P = (O) => {
    const X = c.filter(($) => !$.disabled), N = X.findIndex(($) => $.value === I);
    if (!(N < 0) && (O.key === "ArrowRight" || O.key === "ArrowLeft")) {
      O.preventDefault();
      const $ = O.key === "ArrowRight" ? 1 : -1, E = X[(N + $ + X.length) % X.length];
      E && W(E.value);
    }
  };
  return /* @__PURE__ */ u.jsx("div", { ref: _, className: le("nc-tabs", m), ...x, children: /* @__PURE__ */ u.jsx("div", { className: "nc-tabs__list", role: "tablist", children: c.map((O) => {
    const X = O.value === I;
    return /* @__PURE__ */ u.jsx(
      "button",
      {
        type: "button",
        role: "tab",
        "aria-selected": X,
        tabIndex: X ? 0 : -1,
        disabled: O.disabled,
        className: le("nc-tabs__tab", X && "nc-tabs__tab--active"),
        onClick: () => W(O.value),
        onKeyDown: P,
        children: O.label
      },
      O.value
    );
  }) }) });
});
function _f({
  open: c,
  onClose: f,
  title: s,
  footer: y,
  children: m,
  width: x = 480,
  closeOnBackdrop: _ = !0,
  closeOnEsc: k = !0,
  ariaLabel: M,
  className: I
}) {
  const W = R.useRef(null), P = R.useRef(null), S = R.useId();
  return R.useEffect(() => {
    var $;
    if (!c) return;
    P.current = document.activeElement ?? null;
    const O = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const X = W.current;
    ($ = (X == null ? void 0 : X.querySelector(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    )) ?? X) == null || $.focus();
    const N = (E) => {
      if (k && E.key === "Escape") {
        E.stopPropagation(), f();
        return;
      }
      if (E.key === "Tab" && X) {
        const b = Array.from(
          X.querySelectorAll(
            'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
          )
        ).filter((fe) => !fe.hasAttribute("disabled"));
        if (b.length === 0) {
          E.preventDefault();
          return;
        }
        const G = b[0], ee = b[b.length - 1];
        E.shiftKey && document.activeElement === G ? (E.preventDefault(), ee.focus()) : !E.shiftKey && document.activeElement === ee && (E.preventDefault(), G.focus());
      }
    };
    return document.addEventListener("keydown", N, !0), () => {
      var E, b;
      document.removeEventListener("keydown", N, !0), document.body.style.overflow = O, (b = (E = P.current) == null ? void 0 : E.focus) == null || b.call(E);
    };
  }, [c, f, k]), !c || typeof document > "u" ? null : vf.createPortal(
    /* @__PURE__ */ u.jsxs("div", { className: "nc-modal", role: "presentation", children: [
      /* @__PURE__ */ u.jsx(
        "div",
        {
          className: "nc-modal__backdrop",
          onClick: _ ? f : void 0,
          "aria-hidden": "true"
        }
      ),
      /* @__PURE__ */ u.jsxs(
        "div",
        {
          ref: W,
          className: le("nc-modal__dialog", I),
          role: "dialog",
          "aria-modal": "true",
          "aria-labelledby": s ? S : void 0,
          "aria-label": s ? void 0 : M,
          style: { maxWidth: x },
          tabIndex: -1,
          children: [
            s != null ? /* @__PURE__ */ u.jsxs("div", { className: "nc-modal__header", children: [
              /* @__PURE__ */ u.jsx("h2", { id: S, className: "nc-modal__title", children: s }),
              /* @__PURE__ */ u.jsx("button", { type: "button", className: "nc-modal__close", "aria-label": "close", onClick: f, children: /* @__PURE__ */ u.jsx("svg", { viewBox: "0 0 14 14", width: "14", height: "14", fill: "none", children: /* @__PURE__ */ u.jsx("path", { d: "M3 3 L11 11 M11 3 L3 11", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round" }) }) })
            ] }) : null,
            /* @__PURE__ */ u.jsx("div", { className: "nc-modal__body", children: m }),
            y != null ? /* @__PURE__ */ u.jsx("div", { className: "nc-modal__footer", children: y }) : null
          ]
        }
      )
    ] }),
    document.body
  );
}
R.forwardRef(function({
  title: c,
  icon: f,
  description: s,
  href: y,
  variant: m = "app",
  value: x,
  unit: _,
  delta: k,
  badge: M,
  badgeTone: I = "info",
  status: W,
  disabled: P = !1,
  selected: S = !1,
  external: O = !1,
  size: X = "md",
  as: N,
  className: $,
  onClick: E,
  ...b
}, G) {
  const ee = !!(y || E || N) && !P, fe = P ? "div" : N ?? (y ? "a" : E ? "button" : "div"), ge = `${c} ${s ?? ""}`.trim().toLowerCase(), q = {
    ref: G,
    className: le(
      "nc-tile",
      `nc-tile--${m}`,
      `nc-tile--${X}`,
      S && "nc-tile--selected",
      P && "nc-tile--disabled",
      ee && "nc-tile--interactive",
      $
    ),
    "data-nc-search": ge,
    "aria-disabled": P || void 0,
    ...b
  };
  ee && (q["data-nc-tile"] = "", q.onClick = E, fe === "a" ? (q.href = y, O && (q.target = "_blank", q.rel = "noopener noreferrer")) : fe === "button" && (q.type = "button"));
  const D = /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    /* @__PURE__ */ u.jsxs("div", { className: "nc-tile__top", children: [
      f ? /* @__PURE__ */ u.jsx("span", { className: "nc-tile__icon", children: /* @__PURE__ */ u.jsx(f, { size: 22, strokeWidth: 1.75, "aria-hidden": !0 }) }) : null,
      /* @__PURE__ */ u.jsxs("div", { className: "nc-tile__heading", children: [
        /* @__PURE__ */ u.jsx("span", { className: "nc-tile__title", children: c }),
        ee ? /* @__PURE__ */ u.jsx(Vo, { size: 14, className: "nc-tile__chevron", "aria-hidden": !0 }) : null
      ] }),
      M != null ? /* @__PURE__ */ u.jsx(
        "span",
        {
          className: le(
            "nc-tile__badge",
            `nc-tile__badge--${I}`,
            typeof M == "number" && "nc-tile__badge--count"
          ),
          children: M
        }
      ) : null,
      W ? /* @__PURE__ */ u.jsx(
        "span",
        {
          className: le("nc-tile__status", `nc-tile__status--${W}`),
          role: "img",
          "aria-label": `status: ${W}`
        }
      ) : null
    ] }),
    m === "kpi" ? /* @__PURE__ */ u.jsxs("div", { className: "nc-tile__kpi", children: [
      /* @__PURE__ */ u.jsxs("span", { className: "nc-tile__value", children: [
        x,
        _ ? /* @__PURE__ */ u.jsx("span", { className: "nc-tile__unit", children: _ }) : null
      ] }),
      k ? /* @__PURE__ */ u.jsxs("span", { className: le("nc-tile__delta", `nc-tile__delta--${k.tone ?? "neutral"}`), children: [
        /* @__PURE__ */ u.jsx("span", { className: "nc-tile__delta-arrow", "data-dir": k.direction, "aria-hidden": !0 }),
        k.value
      ] }) : null
    ] }) : s ? /* @__PURE__ */ u.jsx("div", { className: "nc-tile__desc", children: s }) : null
  ] });
  return R.createElement(fe, q, D);
});
const tc = "[data-nc-tile]";
function nc(c) {
  return Array.from(c.querySelectorAll(tc));
}
function jf(c) {
  return nc(c).filter(
    (f) => f.getAttribute("aria-disabled") !== "true" && f.offsetParent !== null
  );
}
function Nf(c, f, s) {
  const y = f.getBoundingClientRect(), m = y.left + y.width / 2, x = y.top + y.height / 2;
  let _, k = 1 / 0;
  for (const M of c) {
    if (M === f) continue;
    const I = M.getBoundingClientRect(), W = I.left + I.width / 2, P = I.top + I.height / 2 - x;
    if (s === "down" && P <= 1 || s === "up" && P >= -1) continue;
    const S = Math.abs(P) + Math.abs(W - m) * 2;
    S < k && (k = S, _ = M);
  }
  return _;
}
function rc(c, f = !0) {
  return R.useEffect(() => {
    if (!f) return;
    const s = c.current;
    if (!s) return;
    const y = nc(s);
    if (!y.length) return;
    const m = y.filter((k) => k.getAttribute("aria-disabled") !== "true"), x = s.querySelector(`${tc}[data-nc-active="true"]`), _ = x && m.includes(x) ? x : m[0];
    for (const k of y) k.tabIndex = k === _ ? 0 : -1;
  }), { onKeyDown: R.useCallback(
    (s) => {
      if (!f) return;
      const y = c.current;
      if (!y || !["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(s.key)) return;
      const m = jf(y);
      if (!m.length) return;
      const x = document.activeElement, _ = x ? m.indexOf(x) : -1;
      let k;
      if (s.key === "Home" ? k = m[0] : s.key === "End" ? k = m[m.length - 1] : _ === -1 ? k = m[0] : s.key === "ArrowRight" ? k = m[Math.min(_ + 1, m.length - 1)] : s.key === "ArrowLeft" ? k = m[Math.max(_ - 1, 0)] : k = Nf(m, m[_], s.key === "ArrowDown" ? "down" : "up"), k && k !== x) {
        s.preventDefault();
        for (const M of m) M.removeAttribute("data-nc-active");
        k.setAttribute("data-nc-active", "true"), x && (x.tabIndex = -1), k.tabIndex = 0, k.focus();
      }
    },
    [c, f]
  ) };
}
const lc = R.createContext(null);
R.forwardRef(function({
  legend: c,
  description: f,
  columns: s = "auto",
  minTileWidth: y = "14rem",
  gap: m = "md",
  action: x,
  navigation: _ = "roving",
  className: k,
  children: M,
  ...I
}, W) {
  const P = R.useContext(lc), S = _ === "roving" && !(P != null && P.rovingManaged), O = R.useRef(null), X = rc(O, S), N = R.useId(), $ = s === "auto" ? { gridTemplateColumns: `repeat(auto-fill, minmax(${y}, 1fr))` } : { gridTemplateColumns: `repeat(${s}, minmax(0, 1fr))` };
  return /* @__PURE__ */ u.jsxs("section", { ref: W, className: le("nc-tilegroup", k), "aria-labelledby": N, ...I, children: [
    /* @__PURE__ */ u.jsxs("div", { className: "nc-tilegroup__head", children: [
      /* @__PURE__ */ u.jsxs("h2", { id: N, className: "nc-tilegroup__legend", children: [
        /* @__PURE__ */ u.jsx(Vo, { size: 14, className: "nc-tilegroup__chev", "aria-hidden": !0 }),
        c
      ] }),
      x ? /* @__PURE__ */ u.jsx("div", { className: "nc-tilegroup__action", children: x }) : null
    ] }),
    f ? /* @__PURE__ */ u.jsx("p", { className: "nc-tilegroup__desc", children: f }) : null,
    /* @__PURE__ */ u.jsx(
      "div",
      {
        ref: O,
        className: le("nc-tilegroup__grid", `nc-tilegroup__grid--gap-${m}`),
        style: $,
        onKeyDown: S ? X.onKeyDown : void 0,
        children: M
      }
    )
  ] });
});
R.forwardRef(function({
  title: c,
  searchable: f = !0,
  searchPlaceholder: s = "search apps…",
  query: y,
  onQueryChange: m,
  navigation: x = "roving",
  density: _ = "comfortable",
  favorites: k,
  emptyState: M,
  className: I,
  children: W,
  ...P
}, S) {
  const O = R.useRef(null), X = R.useRef(null), [N, $] = R.useState(""), E = (y ?? N).trim().toLowerCase(), [b, G] = R.useState(-1), ee = (D) => {
    y === void 0 && $(D), m == null || m(D);
  }, fe = rc(O, x === "roving");
  R.useEffect(() => {
    const D = O.current;
    if (!D) return;
    const pe = Array.from(D.querySelectorAll(".nc-tile"));
    let ae = 0;
    for (const Oe of pe) {
      const rt = Oe.getAttribute("data-nc-search") ?? "", Fe = !E || rt.includes(E);
      Oe.toggleAttribute("hidden", !Fe), Fe && (ae += 1);
    }
    for (const Oe of Array.from(D.querySelectorAll(".nc-tilegroup")))
      Oe.toggleAttribute("hidden", !Oe.querySelector(".nc-tile:not([hidden])"));
    G(E ? ae : -1);
  }, [E, W, k]);
  const ge = (D) => {
    var pe;
    if (D.key === "/" && f && document.activeElement !== X.current) {
      D.preventDefault(), (pe = X.current) == null || pe.focus();
      return;
    }
    x === "roving" && fe.onKeyDown(D);
  }, q = (D) => {
    var pe, ae;
    D.key === "Escape" && (D.preventDefault(), ee(""), (ae = (pe = O.current) == null ? void 0 : pe.querySelector("[data-nc-tile]")) == null || ae.focus());
  };
  return /* @__PURE__ */ u.jsx(lc.Provider, { value: { rovingManaged: x === "roving" }, children: /* @__PURE__ */ u.jsxs(
    "div",
    {
      ref: S,
      className: le("nc-portal", `nc-portal--${_}`, I),
      ...P,
      children: [
        c || f ? /* @__PURE__ */ u.jsxs("div", { className: "nc-portal__bar", children: [
          c ? /* @__PURE__ */ u.jsxs("h1", { className: "nc-portal__title", children: [
            /* @__PURE__ */ u.jsx(Vo, { size: 18, className: "nc-portal__chev", "aria-hidden": !0 }),
            c
          ] }) : /* @__PURE__ */ u.jsx("span", {}),
          f ? /* @__PURE__ */ u.jsx("div", { className: "nc-portal__search", role: "search", children: /* @__PURE__ */ u.jsx(
            "input",
            {
              ref: X,
              type: "text",
              className: "nc-portal__search-input",
              placeholder: s,
              "aria-label": "search apps",
              value: y ?? N,
              onChange: (D) => ee(D.target.value),
              onKeyDown: q
            }
          ) }) : null
        ] }) : null,
        /* @__PURE__ */ u.jsxs("div", { ref: O, className: "nc-portal__body", onKeyDown: ge, children: [
          k ? /* @__PURE__ */ u.jsx("div", { className: "nc-portal__favorites", children: k }) : null,
          W,
          b === 0 ? /* @__PURE__ */ u.jsx("div", { className: "nc-portal__empty", children: M ?? /* @__PURE__ */ u.jsxs("span", { children: [
            "no apps match “",
            y ?? N,
            "”."
          ] }) }) : null
        ] }),
        /* @__PURE__ */ u.jsx("span", { className: "nc-portal__sr", "aria-live": "polite", children: b >= 0 ? `${b} app${b === 1 ? "" : "s"} match` : "" })
      ]
    }
  ) });
});
class Ka extends Error {
  constructor(f, s) {
    super(s), this.status = f;
  }
}
function Ef(c, f, s) {
  async function y(m, x = {}) {
    const _ = await fetch(c + "api/" + m, {
      ...x,
      headers: {
        ...x.headers || {},
        ...f ? { Authorization: "Bearer " + f } : {},
        ...x.body ? { "Content-Type": "application/json" } : {}
      }
    });
    if (_.status === 401)
      throw s(), new Ka(401, "session expired");
    if (!_.ok) throw new Ka(_.status, await _.text() || _.statusText);
    if (_.status !== 204)
      return await _.json();
  }
  return {
    wanted: () => y("wanted"),
    request: (m) => y("wanted", {
      method: "POST",
      body: JSON.stringify({
        tmdbId: m.tmdbId,
        mediaType: m.mediaType,
        title: m.title,
        year: m.year,
        posterUrl: m.posterUrl
      })
    }),
    remove: (m) => y("wanted/" + encodeURIComponent(m), { method: "DELETE" }),
    autograb: (m) => y("wanted/" + encodeURIComponent(m) + "/autograb", { method: "POST" }),
    grabMagnet: (m, x) => y("wanted/" + encodeURIComponent(m) + "/grab", {
      method: "POST",
      body: JSON.stringify({ source: x, adapter: "qbittorrent" })
    }),
    releases: (m) => y("wanted/" + encodeURIComponent(m) + "/releases"),
    pick: (m, x) => y("wanted/" + encodeURIComponent(m) + "/pick", {
      method: "POST",
      body: JSON.stringify(x)
    }),
    discover: (m) => y("discover?q=" + encodeURIComponent(m)),
    downloads: () => y("downloads"),
    clients: () => y("clients"),
    indexers: () => y("indexers"),
    search: (m, x = []) => y(
      "search?q=" + encodeURIComponent(m) + (x.length ? "&indexers=" + x.join(",") : "")
    ),
    grabFound: (m, x) => y("search/grab", {
      method: "POST",
      body: JSON.stringify({ ...m, wantedId: x.wantedId || "", title2: x.title || "" })
    }),
    profiles: () => y("profiles"),
    saveProfile: (m) => y("profiles/" + encodeURIComponent(m.id), {
      method: "PUT",
      body: JSON.stringify(m)
    }),
    deleteProfile: (m) => y("profiles/" + encodeURIComponent(m), { method: "DELETE" }),
    control: (m, x, _) => y(
      `downloads/${encodeURIComponent(m)}/${encodeURIComponent(x)}/${_}`,
      { method: "POST" }
    ),
    config: () => y("config")
  };
}
function Cf(c, f, s) {
  const y = R.useRef(s);
  y.current = s, R.useEffect(() => {
    if (!f) return;
    const m = new AbortController();
    let x = !1;
    async function _() {
      let k = 0;
      for (; !x; ) {
        try {
          const I = await fetch(c + "api/events", {
            headers: { Authorization: "Bearer " + f, Accept: "text/event-stream" },
            signal: m.signal
          });
          if (!I.ok || !I.body) throw new Error("stream " + I.status);
          k = 0;
          const W = I.body.getReader(), P = new TextDecoder();
          let S = "";
          for (; ; ) {
            const { value: O, done: X } = await W.read();
            if (X) break;
            S += P.decode(O, { stream: !0 });
            let N;
            for (; (N = S.indexOf(`

`)) >= 0; ) {
              const $ = S.slice(0, N);
              S = S.slice(N + 2);
              let E = "message", b = "";
              for (const G of $.split(`
`))
                G.startsWith("event: ") ? E = G.slice(7).trim() : G.startsWith("data: ") && (b += G.slice(6));
              if (E === "download" && b)
                try {
                  y.current.onDownload(JSON.parse(b));
                } catch {
                }
              else E === "changed" && y.current.onChanged();
            }
          }
        } catch {
          if (x) return;
        }
        if (x) return;
        const M = Math.min(3e4, 1e3 * 2 ** Math.min(k++, 5));
        await new Promise((I) => setTimeout(I, M)), x || y.current.onReconnect();
      }
    }
    return _(), () => {
      x = !0, m.abort();
    };
  }, [c, f]);
}
function zf(c, f) {
  let s;
  return (...y) => {
    s && clearTimeout(s), s = setTimeout(() => c(...y), f);
  };
}
const Ya = ["B", "KB", "MB", "GB", "TB", "PB"];
function mn(c) {
  if (!c || c <= 0) return "";
  let f = c, s = 0;
  for (; f >= 1024 && s < Ya.length - 1; )
    f /= 1024, s++;
  return `${f >= 10 || s === 0 ? Math.round(f) : f.toFixed(1)} ${Ya[s]}`;
}
function ic(c) {
  return c && c > 0 ? `${mn(c)}/s` : "";
}
function Xa(c) {
  if (c == null || c < 0) return "";
  if (c < 60) return `${c}s`;
  if (c < 3600) return `${Math.round(c / 60)}m`;
  const f = Math.floor(c / 3600);
  return f > 72 ? "—" : `${f}h ${Math.round(c % 3600 / 60)}m`;
}
function Pf(c) {
  return `${Math.max(0, Math.min(100, c || 0)).toFixed(0)}%`;
}
function Ga(c) {
  return c == null ? "" : `${Math.round(c / 10)}%`;
}
function Rf(c) {
  var f;
  if (!c) return [];
  try {
    const s = c.split(".")[1];
    if (!s) return [];
    const y = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
    return ((f = JSON.parse(y).realm_access) == null ? void 0 : f.roles) ?? [];
  } catch {
    return [];
  }
}
function oc(c) {
  if (!c) return "";
  const f = new Date(c).getTime();
  if (!Number.isFinite(f)) return "";
  const s = Math.max(0, Math.round((Date.now() - f) / 1e3));
  return s < 60 ? "just now" : s < 3600 ? `${Math.round(s / 60)}m ago` : s < 86400 ? `${Math.round(s / 3600)}h ago` : `${Math.round(s / 86400)}d ago`;
}
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Lf = (c) => c.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase(), uc = (...c) => c.filter((f, s, y) => !!f && f.trim() !== "" && y.indexOf(f) === s).join(" ").trim();
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
var Tf = {
  xmlns: "http://www.w3.org/2000/svg",
  width: 24,
  height: 24,
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 2,
  strokeLinecap: "round",
  strokeLinejoin: "round"
};
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Mf = R.forwardRef(
  ({
    color: c = "currentColor",
    size: f = 24,
    strokeWidth: s = 2,
    absoluteStrokeWidth: y,
    className: m = "",
    children: x,
    iconNode: _,
    ...k
  }, M) => R.createElement(
    "svg",
    {
      ref: M,
      ...Tf,
      width: f,
      height: f,
      stroke: c,
      strokeWidth: y ? Number(s) * 24 / Number(f) : s,
      className: uc("lucide", m),
      ...k
    },
    [
      ..._.map(([I, W]) => R.createElement(I, W)),
      ...Array.isArray(x) ? x : [x]
    ]
  )
);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Zt = (c, f) => {
  const s = R.forwardRef(
    ({ className: y, ...m }, x) => R.createElement(Mf, {
      ref: x,
      iconNode: f,
      className: uc(`lucide-${Lf(c)}`, y),
      ...m
    })
  );
  return s.displayName = `${c}`, s;
};
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const If = Zt("Download", [
  ["path", { d: "M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4", key: "ih7n3h" }],
  ["polyline", { points: "7 10 12 15 17 10", key: "2ggqvy" }],
  ["line", { x1: "12", x2: "12", y1: "15", y2: "3", key: "1vk2je" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Df = Zt("Link2", [
  ["path", { d: "M9 17H7A5 5 0 0 1 7 7h2", key: "8i5ue5" }],
  ["path", { d: "M15 7h2a5 5 0 1 1 0 10h-2", key: "1b9ql8" }],
  ["line", { x1: "8", x2: "16", y1: "12", y2: "12", key: "1jonct" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Of = Zt("Pause", [
  ["rect", { x: "14", y: "4", width: "4", height: "16", rx: "1", key: "zuxfzm" }],
  ["rect", { x: "6", y: "4", width: "4", height: "16", rx: "1", key: "1okwgv" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Ff = Zt("Play", [
  ["polygon", { points: "6 3 20 12 6 21 6 3", key: "1oa8hb" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const $f = Zt("Save", [
  [
    "path",
    {
      d: "M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z",
      key: "1c8476"
    }
  ],
  ["path", { d: "M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7", key: "1ydtos" }],
  ["path", { d: "M7 3v4a1 1 0 0 0 1 1h7", key: "t51u73" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Wo = Zt("Search", [
  ["circle", { cx: "11", cy: "11", r: "8", key: "4ej97u" }],
  ["path", { d: "m21 21-4.3-4.3", key: "1qie3q" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Uf = Zt("Trash2", [
  ["path", { d: "M3 6h18", key: "d0wm0j" }],
  ["path", { d: "M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6", key: "4alrt4" }],
  ["path", { d: "M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2", key: "v07s0e" }],
  ["line", { x1: "10", x2: "10", y1: "11", y2: "17", key: "1uufr5" }],
  ["line", { x1: "14", x2: "14", y1: "11", y2: "17", key: "xtxkd" }]
]);
/**
 * @license lucide-react v0.469.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */
const Af = Zt("X", [
  ["path", { d: "M18 6 6 18", key: "1bl5f8" }],
  ["path", { d: "m6 6 12 12", key: "d8bk6v" }]
]);
function sc({ value: c }) {
  const f = Math.max(0, Math.min(100, c || 0));
  return /* @__PURE__ */ u.jsx("div", { className: "acq__bar", role: "progressbar", "aria-valuenow": Math.round(f), children: /* @__PURE__ */ u.jsx("div", { className: "acq__bar-fill", style: { width: `${f}%` } }) });
}
function Bf(c) {
  switch (c) {
    case "fulfilled":
    case "completed":
      return "green";
    case "failed":
      return "amber";
    case "downloading":
    case "packaging":
    case "queued":
    case "pending":
      return "blue";
    default:
      return "neutral";
  }
}
function ac({ status: c }) {
  return /* @__PURE__ */ u.jsx(Tt, { tone: Bf(c), dot: !0, children: c });
}
function cc({ d: c }) {
  const f = [
    Pf(c.progressPct),
    c.bytesTotal ? `${mn(c.bytesDone)} / ${mn(c.bytesTotal)}` : mn(c.bytesDone),
    ic(c.speedBps),
    Xa(c.etaSec) && `${Xa(c.etaSec)} left`,
    c.seeders != null ? `${c.seeders} seeds` : Ga(c.health) && `health ${Ga(c.health)}`,
    c.nativeState
  ].filter(Boolean);
  return /* @__PURE__ */ u.jsx("div", { className: "acq__sub", children: f.join(" · ") });
}
function Vf({ clients: c }) {
  return c.length ? /* @__PURE__ */ u.jsx("div", { className: "acq__chips", children: c.map((f) => {
    const s = [];
    f.down_bps > 0 && s.push(`↓ ${ic(f.down_bps)}`), f.free_disk_bytes && s.push(`${mn(f.free_disk_bytes)} free`);
    for (const y of Object.keys(f.detail || {})) s.push(`${y.replace(/_/g, " ")} ${f.detail[y]}`);
    return f.paused && s.push("paused"), !f.reachable && f.error && s.push(f.error), /* @__PURE__ */ u.jsxs(Tt, { tone: f.reachable ? "green" : "amber", dot: !0, children: [
      f.name,
      s.length ? ` · ${s.join(" · ")}` : ""
    ] }, f.name);
  }) }) : null;
}
function Wf({
  api: c,
  wanted: f,
  onClose: s,
  onGrabbed: y
}) {
  const [m, x] = R.useState(null), [_, k] = R.useState(""), [M, I] = R.useState("");
  R.useEffect(() => {
    let S = !0;
    return c.releases(f.id).then((O) => S && x(O)).catch((O) => S && k(String(O.message || O))), () => {
      S = !1;
    };
  }, [c, f.id]);
  async function W(S) {
    I(S.source);
    try {
      await c.pick(f.id, S), y(), s();
    } catch (O) {
      k(String(O.message || O));
    } finally {
      I("");
    }
  }
  const P = [
    {
      key: "title",
      header: "release",
      render: (S) => /* @__PURE__ */ u.jsxs("div", { children: [
        /* @__PURE__ */ u.jsx("div", { className: "acq__mono", children: S.title }),
        /* @__PURE__ */ u.jsx("div", { className: "acq__sub", children: S.reason })
      ] })
    },
    {
      key: "protocol",
      header: "protocol",
      render: (S) => /* @__PURE__ */ u.jsx(Tt, { tone: S.protocol === "usenet" ? "green" : "blue", children: S.protocol === "usenet" ? "NZB" : "torrent" })
    },
    { key: "indexer", header: "indexer" },
    { key: "size", header: "size", align: "right", render: (S) => mn(S.size) },
    {
      key: "seeders",
      header: "seeds",
      align: "right",
      render: (S) => S.protocol === "torrent" ? String(S.seeders) : "—"
    },
    {
      key: "source",
      header: "",
      align: "right",
      render: (S) => /* @__PURE__ */ u.jsx(
        Ve,
        {
          size: "sm",
          variant: S.best ? "primary" : "default",
          loading: M === S.source,
          onClick: () => void W(S),
          children: "grab"
        }
      )
    }
  ];
  return /* @__PURE__ */ u.jsxs(
    _f,
    {
      open: !0,
      onClose: s,
      width: 900,
      title: `releases · ${f.title}${f.year ? ` (${f.year})` : ""}`,
      children: [
        _ && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: _ }),
        !m && !_ && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "searching the indexers…" }),
        m && /* @__PURE__ */ u.jsx(
          zr,
          {
            columns: P,
            rows: m,
            rowKey: (S) => S.source,
            dense: !0,
            empty: /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "no releases found." })
          }
        )
      ]
    }
  );
}
function Hf({
  api: c,
  rows: f,
  downloads: s,
  admin: y,
  autoGrab: m,
  refresh: x
}) {
  const [_, k] = R.useState(""), [M, I] = R.useState(null), [W, P] = R.useState(""), S = (N) => s.find(($) => $.wantedId === N && ($.state === "downloading" || $.state === "queued"));
  async function O(N, $) {
    k(N), P("");
    try {
      await $(), x();
    } catch (E) {
      P(String(E.message || E));
    } finally {
      k("");
    }
  }
  const X = [
    {
      key: "title",
      header: "title",
      render: (N) => /* @__PURE__ */ u.jsxs("div", { children: [
        /* @__PURE__ */ u.jsx("span", { className: "acq__mono", children: N.title }),
        " ",
        /* @__PURE__ */ u.jsx(Ce, { variant: "muted", as: "span", children: N.year || "" }),
        /* @__PURE__ */ u.jsxs("div", { className: "acq__sub", children: [
          "requested ",
          oc(N.requestedAt)
        ] })
      ] })
    },
    { key: "status", header: "status", render: (N) => /* @__PURE__ */ u.jsx(ac, { status: N.status }) },
    {
      key: "detail",
      header: "detail",
      render: (N) => {
        const $ = S(N.id);
        return /* @__PURE__ */ u.jsxs("div", { children: [
          /* @__PURE__ */ u.jsx(Ce, { variant: "muted", as: "div", children: N.detail }),
          $ && /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
            /* @__PURE__ */ u.jsx(sc, { value: $.progressPct }),
            /* @__PURE__ */ u.jsx(cc, { d: $ })
          ] })
        ] });
      }
    },
    {
      key: "id",
      header: "",
      align: "right",
      render: (N) => {
        if (!y) return null;
        const $ = N.status === "pending" || N.status === "failed";
        return /* @__PURE__ */ u.jsxs("div", { className: "acq__actions", children: [
          $ && m && /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
            /* @__PURE__ */ u.jsx(
              Ve,
              {
                size: "sm",
                variant: "primary",
                loading: _ === N.id,
                leading: /* @__PURE__ */ u.jsx(If, { size: 14 }),
                onClick: () => void O(N.id, () => c.autograb(N.id)),
                children: "find & grab"
              }
            ),
            /* @__PURE__ */ u.jsx(
              Ve,
              {
                size: "sm",
                leading: /* @__PURE__ */ u.jsx(Wo, { size: 14 }),
                onClick: () => I(N),
                children: "releases"
              }
            )
          ] }),
          $ && /* @__PURE__ */ u.jsx(
            Ve,
            {
              size: "sm",
              variant: "ghost",
              leading: /* @__PURE__ */ u.jsx(Df, { size: 14 }),
              onClick: () => {
                const E = window.prompt("magnet or .torrent URL");
                E && O(N.id, () => c.grabMagnet(N.id, E));
              },
              children: "magnet"
            }
          ),
          /* @__PURE__ */ u.jsx(
            Ve,
            {
              size: "sm",
              variant: "ghost",
              leading: /* @__PURE__ */ u.jsx(Uf, { size: 14 }),
              onClick: () => {
                window.confirm(`Remove the request for ${N.title}?`) && O(N.id, () => c.remove(N.id));
              },
              children: "remove"
            }
          )
        ] });
      }
    }
  ];
  return /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    W && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: W }),
    /* @__PURE__ */ u.jsx(
      zr,
      {
        columns: X,
        rows: f,
        rowKey: (N) => N.id,
        empty: /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "nothing requested yet." })
      }
    ),
    M && /* @__PURE__ */ u.jsx(
      Wf,
      {
        api: c,
        wanted: M,
        onClose: () => I(null),
        onGrabbed: x
      }
    )
  ] });
}
function Qf({
  api: c,
  rows: f,
  clients: s,
  admin: y,
  refresh: m
}) {
  const [x, _] = R.useState(""), [k, M] = R.useState("");
  async function I(P, S) {
    if (!(S === "cancel" && !window.confirm(`Cancel ${P.title || P.clientJobId}?`))) {
      _(P.adapter + P.clientJobId), M("");
      try {
        await c.control(P.adapter, P.clientJobId, S), m();
      } catch (O) {
        M(String(O.message || O));
      } finally {
        _("");
      }
    }
  }
  const W = [
    {
      key: "title",
      header: "title",
      render: (P) => /* @__PURE__ */ u.jsxs("div", { children: [
        /* @__PURE__ */ u.jsx("span", { className: "acq__mono", children: P.title || P.clientJobId }),
        /* @__PURE__ */ u.jsxs("div", { className: "acq__sub", children: [
          P.adapter,
          P.finishedAt ? ` · finished ${oc(P.finishedAt)}` : "",
          P.error ? ` · ${P.error}` : ""
        ] })
      ] })
    },
    { key: "state", header: "state", render: (P) => /* @__PURE__ */ u.jsx(ac, { status: P.state }) },
    {
      key: "progressPct",
      header: "progress",
      render: (P) => /* @__PURE__ */ u.jsxs("div", { children: [
        /* @__PURE__ */ u.jsx(sc, { value: P.progressPct }),
        /* @__PURE__ */ u.jsx(cc, { d: P })
      ] })
    },
    {
      key: "clientJobId",
      header: "",
      align: "right",
      render: (P) => {
        if (!y) return null;
        const S = P.state === "downloading" || P.state === "queued", O = P.adapter + P.clientJobId;
        return /* @__PURE__ */ u.jsxs("div", { className: "acq__actions", children: [
          S && /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
            /* @__PURE__ */ u.jsx(
              Ve,
              {
                size: "sm",
                variant: "ghost",
                loading: x === O,
                leading: /* @__PURE__ */ u.jsx(Of, { size: 14 }),
                onClick: () => void I(P, "pause"),
                children: "pause"
              }
            ),
            /* @__PURE__ */ u.jsx(
              Ve,
              {
                size: "sm",
                variant: "ghost",
                leading: /* @__PURE__ */ u.jsx(Ff, { size: 14 }),
                onClick: () => void I(P, "resume"),
                children: "resume"
              }
            )
          ] }),
          /* @__PURE__ */ u.jsx(
            Ve,
            {
              size: "sm",
              variant: "ghost",
              leading: /* @__PURE__ */ u.jsx(Af, { size: 14 }),
              onClick: () => void I(P, "cancel"),
              children: "cancel"
            }
          )
        ] });
      }
    }
  ];
  return /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    /* @__PURE__ */ u.jsx(Vf, { clients: s }),
    k && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: k }),
    /* @__PURE__ */ u.jsx(
      zr,
      {
        columns: W,
        rows: f,
        rowKey: (P) => P.adapter + ":" + P.clientJobId,
        empty: /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "no downloads yet." })
      }
    )
  ] });
}
function qf({
  api: c,
  initialQuery: f,
  refresh: s
}) {
  const [y, m] = R.useState(f), [x, _] = R.useState(null), [k, M] = R.useState(!1), [I, W] = R.useState(/* @__PURE__ */ new Set()), [P, S] = R.useState("");
  async function O() {
    if (y.trim()) {
      M(!0), S("");
      try {
        _(await c.discover(y.trim()));
      } catch (N) {
        S(String(N.message || N));
      } finally {
        M(!1);
      }
    }
  }
  async function X(N) {
    try {
      await c.request(N), W(($) => new Set($).add(N.tmdbId)), s();
    } catch ($) {
      S(String($.message || $));
    }
  }
  return /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    /* @__PURE__ */ u.jsxs("div", { className: "acq__searchbar", children: [
      /* @__PURE__ */ u.jsx(
        hn,
        {
          value: y,
          placeholder: "search a movie or show to request…",
          onChange: (N) => m(N.currentTarget.value),
          onKeyDown: (N) => N.key === "Enter" && void O()
        }
      ),
      /* @__PURE__ */ u.jsx(Ve, { variant: "primary", loading: k, leading: /* @__PURE__ */ u.jsx(Wo, { size: 15 }), onClick: () => void O(), children: "search" })
    ] }),
    P && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: P }),
    x && !x.length && /* @__PURE__ */ u.jsxs(Ce, { variant: "muted", children: [
      "nothing found for “",
      y,
      "”."
    ] }),
    /* @__PURE__ */ u.jsx("div", { className: "acq__grid", children: (x || []).map((N) => /* @__PURE__ */ u.jsxs("div", { className: "acq__card", children: [
      N.posterUrl ? /* @__PURE__ */ u.jsx("img", { src: N.posterUrl, alt: "", loading: "lazy" }) : /* @__PURE__ */ u.jsx("div", { className: "acq__poster-blank" }),
      /* @__PURE__ */ u.jsxs("div", { className: "acq__card-body", children: [
        /* @__PURE__ */ u.jsx("div", { className: "acq__mono acq__card-title", children: N.title }),
        /* @__PURE__ */ u.jsxs(Ce, { variant: "muted", as: "div", children: [
          N.year || "",
          " · ",
          N.mediaType
        ] }),
        N.inLibrary ? /* @__PURE__ */ u.jsx(Ve, { size: "sm", disabled: !0, block: !0, children: "in library" }) : /* @__PURE__ */ u.jsx(
          Ve,
          {
            size: "sm",
            variant: "primary",
            block: !0,
            disabled: I.has(N.tmdbId),
            onClick: () => void X(N),
            children: I.has(N.tmdbId) ? "requested" : "request"
          }
        )
      ] })
    ] }, `${N.mediaType}-${N.tmdbId}`)) })
  ] });
}
function Kf({ rows: c, preferUsenet: f }) {
  const s = [
    { key: "name", header: "indexer", render: (x) => /* @__PURE__ */ u.jsx("span", { className: "acq__mono", children: x.name }) },
    {
      key: "protocol",
      header: "protocol",
      render: (x) => /* @__PURE__ */ u.jsx(Tt, { tone: x.protocol === "usenet" ? "green" : "blue", children: x.protocol === "usenet" ? "NZB" : "torrent" })
    },
    {
      key: "enabled",
      header: "state",
      render: (x) => /* @__PURE__ */ u.jsx(Tt, { tone: x.enabled ? "green" : "neutral", dot: !0, children: x.enabled ? "enabled" : "disabled" })
    }
  ], y = c.filter((x) => x.protocol === "usenet" && x.enabled).length, m = c.filter((x) => x.protocol === "torrent" && x.enabled).length;
  return /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    /* @__PURE__ */ u.jsxs(Ce, { variant: "muted", as: "p", children: [
      y,
      " usenet · ",
      m,
      " torrent enabled.",
      " ",
      f ? "Search is NZB-first: the usenet indexers are queried first, and the torrent fan-out only runs when they come back empty." : "Search is torrent-first."
    ] }),
    /* @__PURE__ */ u.jsx(
      zr,
      {
        columns: s,
        rows: c,
        rowKey: (x) => x.name,
        dense: !0,
        empty: /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "no indexers configured." })
      }
    )
  ] });
}
function Yf({
  api: c,
  indexers: f,
  wanted: s,
  admin: y,
  refresh: m
}) {
  const [x, _] = R.useState(""), [k, M] = R.useState(/* @__PURE__ */ new Set()), [I, W] = R.useState(null), [P, S] = R.useState(!1), [O, X] = R.useState(""), [N, $] = R.useState(""), [E, b] = R.useState(""), G = f.filter((D) => D.enabled);
  async function ee() {
    if (x.trim()) {
      S(!0), $(""), b("");
      try {
        W(await c.search(x.trim(), [...k]));
      } catch (D) {
        $(String(D.message || D));
      } finally {
        S(!1);
      }
    }
  }
  async function fe(D) {
    const pe = s.find(
      (ae) => (ae.status === "pending" || ae.status === "failed") && D.title.toLowerCase().includes(ae.title.toLowerCase().slice(0, 12))
    );
    X(D.source), $("");
    try {
      await c.grabFound(D, { wantedId: pe == null ? void 0 : pe.id, title: x.trim() }), b(
        pe ? `grabbing for the existing request “${pe.title}”.` : "grabbing — a request was created so it lands in the catalog."
      ), m();
    } catch (ae) {
      $(String(ae.message || ae));
    } finally {
      X("");
    }
  }
  function ge(D) {
    M((pe) => {
      const ae = new Set(pe);
      return ae.has(D) ? ae.delete(D) : ae.add(D), ae;
    });
  }
  const q = [
    {
      key: "title",
      header: "release",
      render: (D) => /* @__PURE__ */ u.jsxs("div", { className: D.rejected ? "acq__dim" : void 0, children: [
        /* @__PURE__ */ u.jsx("div", { className: "acq__mono", children: D.title }),
        /* @__PURE__ */ u.jsx("div", { className: "acq__sub", children: D.reason })
      ] })
    },
    {
      key: "protocol",
      header: "protocol",
      render: (D) => /* @__PURE__ */ u.jsx(Tt, { tone: D.protocol === "usenet" ? "green" : "blue", children: D.protocol === "usenet" ? "NZB" : "torrent" })
    },
    { key: "indexer", header: "indexer" },
    { key: "size", header: "size", align: "right", render: (D) => mn(D.size) },
    {
      key: "seeders",
      header: "seeds",
      align: "right",
      render: (D) => D.protocol === "torrent" ? String(D.seeders) : "—"
    },
    {
      key: "score",
      header: "score",
      align: "right",
      render: (D) => D.rejected ? /* @__PURE__ */ u.jsx(Tt, { tone: "amber", children: "rejected" }) : /* @__PURE__ */ u.jsx("span", { className: "acq__sub", children: D.score })
    },
    {
      key: "source",
      header: "",
      align: "right",
      render: (D) => y ? /* @__PURE__ */ u.jsx(
        Ve,
        {
          size: "sm",
          variant: D.best ? "primary" : "default",
          loading: O === D.source,
          onClick: () => void fe(D),
          children: "grab"
        }
      ) : null
    }
  ];
  return /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
    /* @__PURE__ */ u.jsxs("div", { className: "acq__searchbar", children: [
      /* @__PURE__ */ u.jsx(
        hn,
        {
          value: x,
          placeholder: "search all indexers — a title, a release name, anything…",
          onChange: (D) => _(D.currentTarget.value),
          onKeyDown: (D) => D.key === "Enter" && void ee()
        }
      ),
      /* @__PURE__ */ u.jsx(Ve, { variant: "primary", loading: P, leading: /* @__PURE__ */ u.jsx(Wo, { size: 15 }), onClick: () => void ee(), children: "search" })
    ] }),
    /* @__PURE__ */ u.jsxs("div", { className: "acq__scope", children: [
      /* @__PURE__ */ u.jsxs(Ce, { variant: "muted", as: "span", children: [
        k.size ? `${k.size} indexer(s) selected` : "all enabled indexers",
        " — tick to narrow the search:"
      ] }),
      /* @__PURE__ */ u.jsx("div", { className: "acq__scope-list", children: G.map((D) => /* @__PURE__ */ u.jsx(
        Ao,
        {
          checked: k.has(D.id),
          onChange: () => ge(D.id),
          label: `${D.name}${D.protocol === "usenet" ? " (NZB)" : ""}`
        },
        D.id
      )) })
    ] }),
    N && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: N }),
    E && /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: E }),
    I && /* @__PURE__ */ u.jsx(
      zr,
      {
        columns: q,
        rows: I,
        rowKey: (D) => D.source,
        dense: !0,
        empty: /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "nothing found." })
      }
    )
  ] });
}
const Xf = ["2160p", "1080p", "720p", "480p"];
function Gf({ api: c, admin: f }) {
  const [s, y] = R.useState([]), [m, x] = R.useState(null), [_, k] = R.useState(!1), [M, I] = R.useState(""), [W, P] = R.useState(!1);
  if (R.useEffect(() => {
    c.profiles().then((E) => {
      y(E), x(E.find((b) => b.isDefault) || E[0] || null);
    }).catch((E) => I(String(E.message || E)));
  }, [c]), M) return /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: M });
  if (!m) return /* @__PURE__ */ u.jsx(Ce, { variant: "muted", children: "no quality profile configured." });
  const S = m.config, O = (E) => x({ ...m, config: { ...S, ...E } });
  async function X() {
    if (m) {
      k(!0), I("");
      try {
        await c.saveProfile(m), P(!0), setTimeout(() => P(!1), 2500);
      } catch (E) {
        I(String(E.message || E));
      } finally {
        k(!1);
      }
    }
  }
  function N(E, b) {
    const G = [...S.resolutions], ee = G.indexOf(E);
    if (ee < 0) return;
    const fe = ee + b;
    fe < 0 || fe >= G.length || ([G[ee], G[fe]] = [G[fe], G[ee]], O({ resolutions: G }));
  }
  function $(E) {
    const b = S.resolutions.includes(E) ? S.resolutions.filter((G) => G !== E) : [...S.resolutions, E];
    O({ resolutions: b });
  }
  return /* @__PURE__ */ u.jsxs("div", { className: "acq__settings", children: [
    /* @__PURE__ */ u.jsxs("div", { className: "acq__settings-head", children: [
      /* @__PURE__ */ u.jsxs("div", { children: [
        /* @__PURE__ */ u.jsx("span", { className: "acq__mono", children: m.name }),
        " ",
        m.isDefault && /* @__PURE__ */ u.jsx(Tt, { tone: "green", children: "default" }),
        /* @__PURE__ */ u.jsxs(Ce, { variant: "muted", as: "div", children: [
          s.length,
          " profile",
          s.length === 1 ? "" : "s",
          " · this one decides what auto-grab picks."
        ] })
      ] }),
      f && /* @__PURE__ */ u.jsx(Ve, { variant: "primary", loading: _, leading: /* @__PURE__ */ u.jsx($f, { size: 14 }), onClick: () => void X(), children: W ? "saved" : "save" })
    ] }),
    /* @__PURE__ */ u.jsx(pn, { label: "prefer protocol", hint: "which kind of source wins when both are available", children: /* @__PURE__ */ u.jsx(
      kf,
      {
        value: S.preferProtocol,
        onChange: (E) => O({ preferProtocol: E.currentTarget.value }),
        options: [
          { label: "usenet (NZB first)", value: "usenet" },
          { label: "torrent first", value: "torrent" },
          { label: "no preference", value: "any" }
        ]
      }
    ) }),
    /* @__PURE__ */ u.jsx(
      pn,
      {
        label: "resolutions",
        hint: "ordered: the first one listed wins. unticked resolutions are allowed but never preferred.",
        children: /* @__PURE__ */ u.jsx("div", { className: "acq__reslist", children: Xf.map((E) => {
          const b = S.resolutions.includes(E), G = S.resolutions.indexOf(E);
          return /* @__PURE__ */ u.jsxs("div", { className: "acq__resrow", children: [
            /* @__PURE__ */ u.jsx(Ao, { checked: b, onChange: () => $(E), label: E }),
            b && /* @__PURE__ */ u.jsxs(u.Fragment, { children: [
              /* @__PURE__ */ u.jsxs(Tt, { tone: G === 0 ? "green" : "neutral", children: [
                "#",
                G + 1
              ] }),
              /* @__PURE__ */ u.jsx(Ve, { size: "sm", variant: "ghost", onClick: () => N(E, -1), children: "↑" }),
              /* @__PURE__ */ u.jsx(Ve, { size: "sm", variant: "ghost", onClick: () => N(E, 1), children: "↓" })
            ] })
          ] }, E);
        }) })
      }
    ),
    /* @__PURE__ */ u.jsx(pn, { label: "preferred codecs", hint: "comma separated, e.g. x265, hevc", children: /* @__PURE__ */ u.jsx(
      hn,
      {
        value: S.preferredCodecs.join(", "),
        onChange: (E) => O({ preferredCodecs: Ja(E.currentTarget.value) })
      }
    ) }),
    /* @__PURE__ */ u.jsx(pn, { label: "reject terms", hint: "a release whose name contains one of these is never grabbed", children: /* @__PURE__ */ u.jsx(
      hn,
      {
        value: S.rejectTerms.join(", "),
        onChange: (E) => O({ rejectTerms: Ja(E.currentTarget.value) })
      }
    ) }),
    /* @__PURE__ */ u.jsxs("div", { className: "acq__settings-row", children: [
      /* @__PURE__ */ u.jsx(pn, { label: "min size (MB)", hint: "anything smaller is treated as junk", children: /* @__PURE__ */ u.jsx(
        hn,
        {
          type: "number",
          value: String(S.minSizeMb),
          onChange: (E) => O({ minSizeMb: Number(E.currentTarget.value) || 0 })
        }
      ) }),
      /* @__PURE__ */ u.jsx(pn, { label: "max size (MB)", hint: "keeps 70 GB remuxes out", children: /* @__PURE__ */ u.jsx(
        hn,
        {
          type: "number",
          value: String(S.maxSizeMb),
          onChange: (E) => O({ maxSizeMb: Number(E.currentTarget.value) || 0 })
        }
      ) }),
      /* @__PURE__ */ u.jsx(pn, { label: "min seeders", hint: "torrents only", children: /* @__PURE__ */ u.jsx(
        hn,
        {
          type: "number",
          value: String(S.minSeeders),
          onChange: (E) => O({ minSeeders: Number(E.currentTarget.value) || 0 })
        }
      ) })
    ] }),
    /* @__PURE__ */ u.jsx(
      Ao,
      {
        checked: S.preferHdr,
        onChange: (E) => O({ preferHdr: E.currentTarget.checked }),
        label: "prefer HDR / Dolby Vision when available"
      }
    )
  ] });
}
function Ja(c) {
  return c.split(",").map((f) => f.trim()).filter(Boolean);
}
const Jf = 3e4, Zf = ["requests", "downloads", "search", "discover", "indexers", "settings"];
function Uo() {
  const c = window.location.hash.replace(/^#\/?/, "").split("?")[0];
  return Zf.includes(c) ? c : "requests";
}
function bf({
  apiBase: c,
  token: f,
  isAdmin: s,
  onUnauthorized: y
}) {
  const [m, x] = R.useState(null), [_, k] = R.useState(Uo), M = R.useCallback((q) => {
    k(q), Uo() !== q && (window.location.hash = "#/" + q);
  }, []);
  R.useEffect(() => {
    const q = () => k(Uo());
    return window.addEventListener("hashchange", q), () => window.removeEventListener("hashchange", q);
  }, []);
  const [I, W] = R.useState([]), [P, S] = R.useState([]), [O, X] = R.useState([]), [N, $] = R.useState([]), E = R.useMemo(
    () => Ef(c, f, () => y == null ? void 0 : y()),
    [c, f, y]
  ), b = R.useMemo(() => typeof s == "boolean" ? s : m ? Rf(f).includes(m.adminRole) : !1, [s, f, m]);
  R.useEffect(() => {
    let q = !0;
    return E.config().then((D) => q && x(D)).catch(() => {
    }), () => {
      q = !1;
    };
  }, [E]);
  const G = R.useCallback(async () => {
    const [q, D] = await Promise.allSettled([E.wanted(), E.downloads()]);
    q.status === "fulfilled" && W(q.value), D.status === "fulfilled" && S(D.value);
  }, [E]), ee = R.useCallback(async () => {
    const [q, D] = await Promise.allSettled([E.clients(), E.indexers()]);
    q.status === "fulfilled" && X(q.value), D.status === "fulfilled" && $(D.value);
  }, [E]);
  R.useEffect(() => {
    if (!f) return;
    G(), ee();
    const q = setInterval(() => {
      G(), ee();
    }, Jf);
    return () => clearInterval(q);
  }, [f, G, ee]);
  const fe = R.useMemo(() => zf(() => void G(), 400), [G]);
  Cf(c, f, {
    // Telemetry arrives every few seconds — apply it in place rather than
    // refetching the whole list for each tick.
    onDownload: (q) => S((D) => {
      const pe = D.findIndex(
        (Oe) => Oe.adapter === q.adapter && Oe.clientJobId === q.clientJobId
      );
      if (pe < 0) return [q, ...D];
      const ae = D.slice();
      return ae[pe] = q, ae;
    }),
    onChanged: fe,
    onReconnect: () => {
      G(), ee();
    }
  });
  const ge = P.filter((q) => q.state === "downloading" || q.state === "queued").length;
  return /* @__PURE__ */ u.jsxs("div", { className: "acq", children: [
    /* @__PURE__ */ u.jsx(
      Sf,
      {
        value: _,
        onChange: M,
        items: [
          { value: "requests", label: `requests${I.length ? ` (${I.length})` : ""}` },
          { value: "downloads", label: `downloads${ge ? ` (${ge})` : ""}` },
          { value: "search", label: "search" },
          { value: "discover", label: "discover" },
          { value: "indexers", label: "indexers" },
          { value: "settings", label: "settings" }
        ]
      }
    ),
    /* @__PURE__ */ u.jsxs("main", { className: "acq__main", children: [
      _ === "requests" && /* @__PURE__ */ u.jsx(
        Hf,
        {
          api: E,
          rows: I,
          downloads: P,
          admin: b,
          autoGrab: !!(m != null && m.autoGrab),
          refresh: () => void G()
        }
      ),
      _ === "downloads" && /* @__PURE__ */ u.jsx(
        Qf,
        {
          api: E,
          rows: P,
          clients: O,
          admin: b,
          refresh: () => void G()
        }
      ),
      _ === "discover" && /* @__PURE__ */ u.jsx(
        qf,
        {
          api: E,
          initialQuery: new URLSearchParams(window.location.search).get("q") || "",
          refresh: () => void G()
        }
      ),
      _ === "search" && /* @__PURE__ */ u.jsx(
        Yf,
        {
          api: E,
          indexers: N,
          wanted: I,
          admin: b,
          refresh: () => void G()
        }
      ),
      _ === "indexers" && /* @__PURE__ */ u.jsx(Kf, { rows: N, preferUsenet: !0 }),
      _ === "settings" && /* @__PURE__ */ u.jsx(Gf, { api: E, admin: b })
    ] })
  ] });
}
const Za = "acquire-console-styles";
function ep() {
  if (typeof document > "u" || document.getElementById(Za)) return;
  const c = document.createElement("link");
  c.id = Za, c.rel = "stylesheet", c.href = new URL("./console.css", import.meta.url).href, document.head.appendChild(c);
}
function tp(c, f) {
  ep();
  const s = mf.createRoot(c);
  let y = f;
  const m = () => s.render(
    /* @__PURE__ */ u.jsx(R.StrictMode, { children: /* @__PURE__ */ u.jsx(
      bf,
      {
        apiBase: y.apiBase,
        token: y.token,
        isAdmin: y.isAdmin,
        onUnauthorized: y.onUnauthorized
      }
    ) })
  );
  return m(), {
    update(x) {
      y = { ...y, ...x }, m();
    },
    unmount() {
      s.unmount();
    }
  };
}
export {
  tp as default,
  tp as mount
};
