// Local rules for the effect and deferral budget. Each has its own rule id so
// the suppressions file counts them apart.

const HOOKS_DIR = 'web/src/app/hooks/';

const isGlobalCall = (node, name) =>
  node.type === 'CallExpression' &&
  ((node.callee.type === 'Identifier' && node.callee.name === name) ||
    (node.callee.type === 'MemberExpression' &&
      !node.callee.computed &&
      node.callee.object.type === 'Identifier' &&
      (node.callee.object.name === 'window' || node.callee.object.name === 'globalThis') &&
      node.callee.property.name === name));

const noRawEffect = {
  meta: { type: 'problem', schema: [] },
  create(context) {
    return {
      CallExpression(node) {
        const callee = node.callee;
        const name = callee.type === 'Identifier' ? callee.name : callee.type === 'MemberExpression' && !callee.computed ? callee.property.name : '';
        if (name !== 'useEffect' && name !== 'useLayoutEffect') return;
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
    const report = (node, what) => context.report({ node, message: `${what} defers work to settle or focus. ${FIX}` });
    return {
      CallExpression(node) {
        if (isGlobalCall(node, 'requestAnimationFrame')) return report(node, 'requestAnimationFrame');
        if (!isGlobalCall(node, 'setTimeout')) return;
        const delay = node.arguments[1];
        if (!delay || (delay.type === 'Literal' && delay.value === 0)) return report(node, 'setTimeout with no delay');
        const resolver = node.arguments[0];
        if (resolver?.type !== 'Identifier') return;
        const inSleep = context.sourceCode.getAncestors(node).some(
          (a) =>
            a.type === 'NewExpression' &&
            a.callee.type === 'Identifier' &&
            a.callee.name === 'Promise' &&
            a.arguments[0]?.params?.[0]?.type === 'Identifier' &&
            a.arguments[0].params[0].name === resolver.name,
        );
        if (inSleep) report(node, 'A sleep promise');
      },
    };
  },
};

export default { rules: { 'no-raw-effect': noRawEffect, 'no-deferral': noDeferral } };
