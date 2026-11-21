// Local rules for the effect and deferral budget. Each has its own rule id so
// the suppressions file counts them apart. They match syntax only, no types.

const HOOKS_DIR = 'web/src/app/hooks/';
const EFFECTS = new Set(['useEffect', 'useLayoutEffect']);
const HOOK_MODULES = new Set(['preact/hooks', 'preact/compat', 'react']);
const GLOBALS = new Set(['window', 'self', 'globalThis']);
const WRAPPERS = new Set(['TSAsExpression', 'TSNonNullExpression', 'TSSatisfiesExpression', 'TSTypeAssertion']);

const unwrap = (node) => {
  while (node && WRAPPERS.has(node.type)) node = node.expression;
  return node;
};

const propertyName = (member) => {
  if (!member.computed) return member.property.type === 'Identifier' ? member.property.name : '';
  return member.property.type === 'Literal' && typeof member.property.value === 'string' ? member.property.value : '';
};

const isGlobalCall = (node, name) => {
  const callee = unwrap(node.callee);
  if (callee.type === 'Identifier') return callee.name === name;
  if (callee.type !== 'MemberExpression' || propertyName(callee) !== name) return false;
  const object = unwrap(callee.object);
  return object.type === 'Identifier' && GLOBALS.has(object.name);
};

const mentions = (node, name, keys) => {
  if (!node || typeof node.type !== 'string') return false;
  if (node.type === 'Identifier' && node.name === name) return true;
  return (keys[node.type] ?? []).some((k) => {
    const child = node[k];
    return Array.isArray(child) ? child.some((c) => mentions(c, name, keys)) : mentions(child, name, keys);
  });
};

const noRawEffect = {
  meta: { type: 'problem', schema: [] },
  create(context) {
    const aliases = new Set(EFFECTS);
    return {
      ImportDeclaration(node) {
        if (!HOOK_MODULES.has(node.source.value)) return;
        for (const s of node.specifiers) {
          if (s.type === 'ImportSpecifier' && EFFECTS.has(s.imported.name ?? s.imported.value)) aliases.add(s.local.name);
        }
      },
      CallExpression(node) {
        const callee = unwrap(node.callee);
        const name = callee.type === 'Identifier' ? callee.name : callee.type === 'MemberExpression' ? propertyName(callee) : '';
        if (callee.type === 'Identifier' ? !aliases.has(name) : !EFFECTS.has(name)) return;
        context.report({
          node,
          message: `A raw ${name} is not allowed in a component file. Use a hook from ${HOOKS_DIR} (useFocusOnOpen, useFocusOnClose, usePollUntil, useSubscribe), or add one there for a shape that repeats.`,
        });
      },
    };
  },
};

const FIX = `Use a hook from ${HOOKS_DIR}: useFocusOnOpen or useFocusOnClose to focus after render, usePollUntil to wait for a condition.`;

const noDeferral = {
  meta: { type: 'problem', schema: [] },
  create(context) {
    const source = context.sourceCode;
    const report = (node, what) => context.report({ node, message: `${what} defers work to settle or focus. ${FIX}` });
    return {
      CallExpression(node) {
        if (isGlobalCall(node, 'requestAnimationFrame')) return report(node, 'requestAnimationFrame');
        if (isGlobalCall(node, 'queueMicrotask')) return report(node, 'queueMicrotask');
        if (!isGlobalCall(node, 'setTimeout')) return;
        const delay = unwrap(node.arguments[1]);
        const none = !delay || (delay.type === 'Literal' && delay.value === 0) || (delay.type === 'Identifier' && delay.name === 'undefined');
        if (none) return report(node, 'setTimeout with no delay');
        const sleeping = source.getAncestors(node).some((a) => {
          if (a.type !== 'NewExpression' || a.callee.type !== 'Identifier' || a.callee.name !== 'Promise') return false;
          const executor = a.arguments[0];
          const resolver = executor?.params?.[0];
          return resolver?.type === 'Identifier' && mentions(node.arguments[0], resolver.name, source.visitorKeys);
        });
        if (sleeping) report(node, 'A sleep promise');
      },
    };
  },
};

export default { rules: { 'no-raw-effect': noRawEffect, 'no-deferral': noDeferral } };
