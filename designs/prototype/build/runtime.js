/*
 * Minimal runtime for the Admiral prototype.
 *
 * The prototype was designed as a Design Component (.dc.html): an HTML
 * template with {{holes}}, <sc-if>, <sc-for> and a `class Component extends
 * DCLogic` whose renderVals() feeds the template. This file renders that same
 * template with React so the prototype runs in any browser, outside the design
 * canvas. It is a reference for behaviour and look, not production code.
 */
(function () {
  'use strict';
  var h = React.createElement;

  var EVENT = {
    onclick: 'onClick', onpointerdown: 'onPointerDown', onpointermove: 'onPointerMove',
    onpointerup: 'onPointerUp', onpointercancel: 'onPointerCancel', onchange: 'onChange',
    onsubmit: 'onSubmit', onfocus: 'onFocus', onblur: 'onBlur', onkeydown: 'onKeyDown'
  };
  var HTML_ATTR = {
    'class': 'className', 'for': 'htmlFor', autocomplete: 'autoComplete',
    spellcheck: 'spellCheck', tabindex: 'tabIndex', readonly: 'readOnly'
  };
  var VOID = { input: 1, img: 1, br: 1, hr: 1, meta: 1, link: 1 };
  var WHOLE = /^\s*\{\{\s*([^}]+?)\s*\}\}\s*$/;
  var ANY = /\{\{\s*([^}]+?)\s*\}\}/g;

  function camel(s) { return s.replace(/-([a-z])/g, function (_, c) { return c.toUpperCase(); }); }

  function lookup(path, scopes) {
    if (path === 'true') return true;
    if (path === 'false') return false;
    if (/^-?\d+(\.\d+)?$/.test(path)) return Number(path);
    var parts = path.split('.');
    var head = parts[0];
    var val;
    for (var i = scopes.length - 1; i >= 0; i--) {
      if (scopes[i] && Object.prototype.hasOwnProperty.call(scopes[i], head)) { val = scopes[i][head]; break; }
    }
    for (var j = 1; j < parts.length && val != null; j++) val = val[parts[j]];
    return val;
  }
  function interp(str, scopes) {
    return str.replace(ANY, function (_, p) { var v = lookup(p, scopes); return v == null ? '' : String(v); });
  }
  function value(str, scopes) {
    var m = str.match(WHOLE);
    if (m) return lookup(m[1], scopes);
    return str.indexOf('{{') === -1 ? str : interp(str, scopes);
  }
  function parseStyle(s) {
    var out = {};
    if (!s) return out;
    String(s).split(';').forEach(function (decl) {
      var i = decl.indexOf(':');
      if (i < 0) return;
      var k = decl.slice(0, i).trim();
      var v = decl.slice(i + 1).trim();
      if (!k) return;
      out[k.indexOf('--') === 0 ? k : camel(k)] = v;
    });
    return out;
  }

  function children(node, scopes) {
    var out = [];
    node.childNodes.forEach(function (c, i) {
      var r = render(c, scopes, i);
      if (r == null) return;
      if (Array.isArray(r)) r.forEach(function (x) { out.push(x); });
      else out.push(r);
    });
    return out;
  }

  function render(node, scopes, key) {
    if (node.nodeType === 3) {
      var t = node.nodeValue;
      if (!/\S/.test(t) && t.indexOf('\n') !== -1) return null;
      return t.indexOf('{{') === -1 ? t : interp(t, scopes);
    }
    if (node.nodeType !== 1) return null;
    var tag = node.localName;

    if (tag === 'sc-if') {
      if (!lookup((node.getAttribute('value') || '').replace(/^\s*\{\{\s*|\s*\}\}\s*$/g, ''), scopes)) return null;
      return h(React.Fragment, { key: key }, children(node, scopes));
    }
    if (tag === 'sc-for') {
      var list = lookup((node.getAttribute('list') || '').replace(/^\s*\{\{\s*|\s*\}\}\s*$/g, ''), scopes) || [];
      var as = node.getAttribute('as') || 'item';
      return h(React.Fragment, { key: key }, list.map(function (item, i) {
        var scope = { $index: i };
        scope[as] = item;
        return h(React.Fragment, { key: i }, children(node, scopes.concat([scope])));
      }));
    }

    var props = { key: key };
    var isSvg = node.namespaceURI === 'http://www.w3.org/2000/svg';
    for (var a = 0; a < node.attributes.length; a++) {
      var at = node.attributes[a];
      var name = at.name;
      if (name.indexOf('hint-') === 0) continue;
      var v = value(at.value, scopes);
      var lower = name.toLowerCase();
      if (EVENT[lower]) { if (typeof v === 'function') props[EVENT[lower]] = v; continue; }
      if (name === 'style') { props.style = parseStyle(v); continue; }
      if (HTML_ATTR[lower]) { props[HTML_ATTR[lower]] = v; continue; }
      if (lower === 'value' && tag === 'input') { props.value = v == null ? '' : v; continue; }
      if (isSvg && name.indexOf('-') !== -1 && name.indexOf('data-') !== 0 && name.indexOf('aria-') !== 0) { props[camel(name)] = v; continue; }
      props[name] = v;
    }
    if (VOID[tag]) return h(tag, props);
    return h(tag, props, children(node, scopes));
  }

  window.DCLogic = class DCLogic extends React.Component {
    render() {
      var vals = this.renderVals() || {};
      var tpl = document.getElementById('dc-template').content;
      var nodes = children(tpl, [vals]);
      return h(React.Fragment, null, nodes);
    }
  };

  window.mountDC = function (Component) {
    var params = new URLSearchParams(location.search);
    var props = {};
    params.forEach(function (v, k) { props[k] = v; });
    ReactDOM.createRoot(document.getElementById('root')).render(h(Component, props));
  };
})();
