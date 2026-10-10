const { expect } = require("@playwright/test");

async function assertAccessibleControls(page, testInfo, state) {
  const cdp = await page.context().newCDPSession(page);
  try {
    const { nodes } = await cdp.send("Accessibility.getFullAXTree");
    const roles = new Set(["button", "checkbox", "switch", "radio", "combobox", "textbox", "searchbox", "link", "tab"]);
    const unnamed = [];
    for (const node of nodes) {
      if (node.ignored || !roles.has(node.role?.value) || node.name?.value?.trim()) continue;
      const { outerHTML } = await cdp.send("DOM.getOuterHTML", { backendNodeId: node.backendDOMNodeId });
      unnamed.push({ role: node.role.value, html: outerHTML.slice(0, 500) });
    }
    await testInfo.attach(`accessibility-${state}`, {
      body: JSON.stringify({ controls: nodes.filter((node) => !node.ignored && roles.has(node.role?.value)).length, unnamed }, null, 2),
      contentType: "application/json",
    });
    expect(unnamed, `${state}: 접근성 이름 없는 컨트롤`).toEqual([]);
  } finally {
    await cdp.detach();
  }
}

module.exports = { assertAccessibleControls };
