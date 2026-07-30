(function (window, document) {
  "use strict";

  var VERSION = 1;
  var SVG_NS = "http://www.w3.org/2000/svg";
  var OPT_OUT_PREFIX = "araihu.assets.campaign.v1.optout.";
  var root = document.documentElement;
  var script = document.currentScript;
  var configuredChannel = (script && script.dataset.channel) || "/assets/releases/current";
  var channelURL;
  var activeOperation = null;
  var pendingOperations = [];
  var state = null;
  var lastChannel = null;
  var selfThemeSource = null;

  function RuntimeError(code) {
    this.code = code;
  }

  function fail(code) {
    throw new RuntimeError(code);
  }

  function isObject(value) {
    return value !== null && typeof value === "object" && !Array.isArray(value);
  }

  function hasOnlyKeys(value, required, optional) {
    if (!isObject(value)) {
      return false;
    }
    var allowed = required.concat(optional || []);
    var keys = Object.keys(value);
    return required.every(function (key) {
      return Object.prototype.hasOwnProperty.call(value, key);
    }) && keys.every(function (key) {
      return allowed.indexOf(key) !== -1;
    });
  }

  function isName(value) {
    return typeof value === "string" && /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(value);
  }

  function isRelease(value) {
    return typeof value === "string" &&
      /^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(value);
  }

  function isLocalHost(hostname) {
    return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]";
  }

  function resolveChannelURL() {
    var resolved;
    try {
      resolved = new window.URL(configuredChannel, document.baseURI || window.location.href);
    } catch (_) {
      fail("channel-url");
    }
    if ((resolved.protocol !== "https:" && !(resolved.protocol === "http:" && isLocalHost(resolved.hostname))) ||
        resolved.username || resolved.password || resolved.search || resolved.hash ||
        resolved.pathname.indexOf("/assets/releases/") !== 0) {
      fail("channel-url");
    }
    return resolved;
  }

  function validateAssetURL(raw, release) {
    if (typeof raw !== "string" || /%2e|%2f|%5c/i.test(raw)) {
      fail("asset-url");
    }
    var resolved;
    try {
      resolved = new window.URL(raw);
    } catch (_) {
      fail("asset-url");
    }
    if (resolved.protocol !== channelURL.protocol ||
        resolved.origin !== channelURL.origin ||
        resolved.username || resolved.password || resolved.search || resolved.hash ||
        resolved.pathname.indexOf("/assets/releases/" + release + "/") !== 0) {
      fail("asset-url");
    }
    return resolved.href;
  }

  function validateTheme(theme, release) {
    if (!hasOnlyKeys(theme, ["id", "cssUrl"]) || !isName(theme.id)) {
      fail("channel-schema");
    }
    theme.cssUrl = validateAssetURL(theme.cssUrl, release);
  }

  function validateIcon(icon, release) {
    var optional = icon && icon.mode === "sprite" ? ["spriteSymbol"] : [];
    if (!hasOnlyKeys(icon, ["id", "mode", "url"], optional) ||
        !isName(icon.id) ||
        (icon.mode !== "asset" && icon.mode !== "sprite")) {
      fail("channel-schema");
    }
    if (icon.mode === "sprite" && !isName(icon.spriteSymbol)) {
      fail("channel-schema");
    }
    if (icon.mode === "asset" && Object.prototype.hasOwnProperty.call(icon, "spriteSymbol")) {
      fail("channel-schema");
    }
    icon.url = validateAssetURL(icon.url, release);
  }

  function validateResolvedAsset(asset, release) {
    if (!hasOnlyKeys(asset, ["id", "url"]) || !isName(asset.id)) {
      fail("channel-schema");
    }
    asset.url = validateAssetURL(asset.url, release);
  }

  function validateCampaign(campaign, release) {
    if (!hasOnlyKeys(campaign, ["id", "toggle", "brand"]) || !isName(campaign.id) ||
        !hasOnlyKeys(campaign.toggle, ["enabledIcon", "disabledIcon"]) ||
        !hasOnlyKeys(campaign.brand, ["logo", "icon"])) {
      fail("channel-schema");
    }
    validateIcon(campaign.toggle.enabledIcon, release);
    validateIcon(campaign.toggle.disabledIcon, release);
    validateResolvedAsset(campaign.brand.logo, release);
    validateResolvedAsset(campaign.brand.icon, release);
  }

  function validateChannel(channel) {
    if (!isObject(channel)) {
      fail("channel-schema");
    }
    if (channel.schemaVersion !== 1) {
      fail("schema-version");
    }
    if (channel.runtimeVersion !== VERSION) {
      fail("runtime-version");
    }
    var optional = channel.source === "campaign" ? ["campaign"] : [];
    if (!hasOnlyKeys(channel, ["schemaVersion", "runtimeVersion", "release", "source", "theme", "digest"], optional) ||
        !isRelease(channel.release) ||
        (channel.source !== "default" && channel.source !== "campaign") ||
        typeof channel.digest !== "string" || !/^[0-9a-f]{64}$/.test(channel.digest)) {
      fail("channel-schema");
    }
    validateTheme(channel.theme, channel.release);
    if (channel.source === "campaign") {
      validateCampaign(channel.campaign, channel.release);
    }
    return channel;
  }

  function request(url) {
    return window.fetch(url, {
      credentials: "omit",
      mode: "cors"
    });
  }

  async function fetchChannel() {
    var response;
    try {
      response = await request(channelURL.href);
    } catch (_) {
      fail("channel-fetch");
    }
    if (!response || !response.ok) {
      fail("channel-fetch");
    }
    var channel;
    try {
      channel = await response.json();
    } catch (_) {
      fail("channel-parse");
    }
    return validateChannel(channel);
  }

  function reducedMotion() {
    return Boolean(window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  function detail(code, campaign) {
    return Object.freeze({
      code: code,
      campaign: campaign,
      reducedMotion: reducedMotion()
    });
  }

  function dispatch(type, code, campaign) {
    document.dispatchEvent(new window.CustomEvent(type, {
      detail: detail(code, campaign)
    }));
  }

  function dispatchError(code) {
    document.dispatchEvent(new window.CustomEvent("araihu:campaign:error", {
      detail: Object.freeze({ code: code })
    }));
  }

  function setThemeSource(source) {
    selfThemeSource = source;
    root.setAttribute("data-theme-source", source);
  }

  function all(selector) {
    return Array.prototype.slice.call(document.querySelectorAll(selector));
  }

  function brandHooks() {
    return {
      logos: all('[data-asset-brand="logo"]'),
      icons: all('[data-asset-brand="icon"]'),
      toggles: all("[data-campaign-toggle]"),
      toggleIcons: all("[data-campaign-toggle-icon]")
    };
  }

  function captureCrossOrigin(node) {
    return {
      present: node.hasAttribute("crossorigin"),
      value: node.getAttribute("crossorigin")
    };
  }

  function restoreCrossOrigin(node, captured) {
    if (captured.present) {
      node.setAttribute("crossorigin", captured.value);
    } else {
      node.removeAttribute("crossorigin");
    }
  }

  function captureBaseline() {
    var hooks = brandHooks();
    return {
      theme: root.getAttribute("data-theme"),
      source: root.getAttribute("data-theme-source") || "default",
      logos: hooks.logos.map(function (node) { return node.src; }),
      logoCrossOrigins: hooks.logos.map(captureCrossOrigin),
      icons: hooks.icons.map(function (node) { return node.href; }),
      iconCrossOrigins: hooks.icons.map(captureCrossOrigin),
      toggleHidden: hooks.toggles.map(function (node) { return node.hidden; }),
      togglePressed: hooks.toggles.map(function (node) { return node.getAttribute("aria-pressed"); }),
      toggleChildren: hooks.toggleIcons.map(function (node) {
        return Array.prototype.map.call(node.children, function (child) {
          return document.importNode(child, true);
        });
      })
    };
  }

  function setBrand(urls) {
    var hooks = brandHooks();
    hooks.logos.forEach(function (node) {
      node.crossOrigin = "anonymous";
      node.src = urls.logo;
    });
    hooks.icons.forEach(function (node) {
      node.crossOrigin = "anonymous";
      node.href = urls.icon;
    });
  }

  function restoreBrand(baseline) {
    var hooks = brandHooks();
    hooks.logos.forEach(function (node, index) {
      restoreCrossOrigin(node, baseline.logoCrossOrigins[index]);
      node.src = baseline.logos[index];
    });
    hooks.icons.forEach(function (node, index) {
      restoreCrossOrigin(node, baseline.iconCrossOrigins[index]);
      node.href = baseline.icons[index];
    });
  }

  function restoreToggle(baseline) {
    var hooks = brandHooks();
    hooks.toggles.forEach(function (node, index) {
      node.hidden = baseline.toggleHidden[index];
      var pressed = baseline.togglePressed[index];
      if (pressed === null) {
        node.removeAttribute("aria-pressed");
      } else {
        node.setAttribute("aria-pressed", pressed);
      }
    });
    hooks.toggleIcons.forEach(function (node, index) {
      var children = baseline.toggleChildren[index].map(function (child) {
        return document.importNode(child, true);
      });
      node.replaceChildren.apply(node, children);
    });
  }

  function showToggle(pressed, preparedIcon) {
    var hooks = brandHooks();
    hooks.toggles.forEach(function (node) {
      node.hidden = false;
      node.setAttribute("aria-pressed", pressed ? "true" : "false");
    });
    hooks.toggleIcons.forEach(function (node) {
      node.replaceChildren(renderIcon(preparedIcon));
    });
  }

  function removeStyle(activeState) {
    if (activeState && activeState.style) {
      activeState.style.remove();
      activeState.style = null;
    }
  }

  function restoreState(activeState, keepThemeAndSource) {
    removeStyle(activeState);
    restoreBrand(activeState.baseline);
    restoreToggle(activeState.baseline);
    root.removeAttribute("data-campaign");
    if (!keepThemeAndSource) {
      if (activeState.baseline.theme === null) {
        root.removeAttribute("data-theme");
      } else {
        root.setAttribute("data-theme", activeState.baseline.theme);
      }
      setThemeSource(activeState.baseline.source);
    }
  }

  function preloadStyle(url) {
    var link = document.createElement("link");
    var rejectLoad;
    var settled = false;
    var promise = new Promise(function (resolve, reject) {
      rejectLoad = reject;
      link.onload = function () {
        if (!settled) {
          settled = true;
          resolve(link);
        }
      };
      link.onerror = function () {
        if (!settled) {
          settled = true;
          link.remove();
          reject(new RuntimeError("theme-load"));
        }
      };
    });
    link.rel = "stylesheet";
    link.media = "not all";
    link.crossOrigin = "anonymous";
    link.href = url;
    document.head.appendChild(link);
    return {
      promise: promise,
      cancel: function () {
        link.remove();
        if (!settled) {
          settled = true;
          rejectLoad(new RuntimeError("theme-load"));
        }
      }
    };
  }

  async function preloadImage(url) {
    var image = new window.Image();
    image.crossOrigin = "anonymous";
    image.src = url;
    try {
      if (typeof image.decode === "function") {
        await image.decode();
      } else {
        await new Promise(function (resolve, reject) {
          image.onload = resolve;
          image.onerror = reject;
        });
      }
    } catch (_) {
      fail("image-load");
    }
    return { mode: "asset", url: url };
  }

  function safeSVGTree(node) {
    var allowedElements = ["circle", "ellipse", "g", "line", "path", "polygon", "polyline", "rect"];
    var allowedAttributes = [
      "class", "clip-path", "cx", "cy", "d", "fill", "fill-rule", "height",
      "points", "r", "rx", "ry", "stroke", "stroke-linecap", "stroke-linejoin",
      "stroke-width", "transform", "viewBox", "width", "x", "x1", "x2", "y", "y1", "y2"
    ];
    if (!node || node.namespaceURI !== SVG_NS || allowedElements.indexOf(node.localName) === -1) {
      return false;
    }
    var attributes = Array.prototype.slice.call(node.attributes || []);
    if (node.attributes && typeof node.attributes.entries === "function") {
      attributes = Array.from(node.attributes.entries()).map(function (entry) {
        return { name: entry[0], value: entry[1] };
      });
    }
    if (attributes.some(function (attribute) {
      var name = attribute.name;
      var value = attribute.value;
      return allowedAttributes.indexOf(name) === -1 ||
        /^on/i.test(name) || /(?:javascript:|url\s*\()/i.test(value);
    })) {
      return false;
    }
    return Array.prototype.every.call(node.children || [], safeSVGTree);
  }

  async function preloadSprite(icon) {
    var response;
    try {
      response = await request(icon.url);
    } catch (_) {
      fail("sprite-fetch");
    }
    if (!response || !response.ok) {
      fail("sprite-fetch");
    }
    var text;
    try {
      text = await response.text();
    } catch (_) {
      fail("sprite-fetch");
    }
    var parsed;
    try {
      parsed = new window.DOMParser().parseFromString(text, "image/svg+xml");
    } catch (_) {
      fail("sprite-parse");
    }
    if (!parsed || parsed.querySelector("parsererror")) {
      fail("sprite-parse");
    }
    var symbol = parsed.getElementById(icon.spriteSymbol);
    if (!symbol || symbol.namespaceURI !== SVG_NS || symbol.localName !== "symbol" ||
        symbol.getAttribute("id") !== icon.spriteSymbol ||
        !Array.prototype.every.call(symbol.children, safeSVGTree)) {
      fail("sprite-parse");
    }
    var viewBox = symbol.getAttribute("viewBox");
    if (viewBox !== null && !/^-?[0-9.]+(?: +-?[0-9.]+){3}$/.test(viewBox)) {
      fail("sprite-parse");
    }
    return {
      mode: "sprite",
      symbol: symbol,
      viewBox: viewBox
    };
  }

  function preloadIcon(icon) {
    return icon.mode === "sprite" ? preloadSprite(icon) : preloadImage(icon.url);
  }

  function renderIcon(prepared) {
    if (prepared.mode === "asset") {
      var image = document.createElement("img");
      image.crossOrigin = "anonymous";
      image.src = prepared.url;
      image.alt = "";
      image.setAttribute("aria-hidden", "true");
      return image;
    }
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    if (prepared.viewBox !== null) {
      svg.setAttribute("viewBox", prepared.viewBox);
    }
    Array.prototype.forEach.call(prepared.symbol.children, function (child) {
      svg.appendChild(document.importNode(child, true));
    });
    return svg;
  }

  function optOutKey(campaignID) {
    return OPT_OUT_PREFIX + campaignID;
  }

  function isOptedOut(campaignID) {
    try {
      return window.localStorage.getItem(optOutKey(campaignID)) === "1";
    } catch (_) {
      fail("storage-read");
    }
  }

  async function prepareCampaign(channel, baseline) {
    var campaign = channel.campaign;
    var stagedStyle = preloadStyle(channel.theme.cssUrl);
    try {
      var prepared = await Promise.all([
        stagedStyle.promise,
        preloadImage(campaign.brand.logo.url),
        preloadImage(campaign.brand.icon.url),
        preloadIcon(campaign.toggle.enabledIcon)
      ]);
      return {
        baseline: baseline,
        campaign: campaign,
        digest: channel.digest,
        style: prepared[0],
        enabledIcon: prepared[3],
        mode: "campaign"
      };
    } catch (error) {
      stagedStyle.cancel();
      throw error;
    }
  }

  async function applyCampaign(channel) {
    var campaign = channel.campaign;
    if (root.getAttribute("data-theme-source") === "preference") {
      return false;
    }
    var optedOut = isOptedOut(campaign.id);
    if (state && state.campaign.id === campaign.id && state.digest === channel.digest &&
        ((state.mode === "campaign" && !optedOut) || (state.mode === "optout" && optedOut))) {
      return state.mode === "campaign";
    }
    var baseline = state ? state.baseline : captureBaseline();
    if (optedOut) {
      var optOutState = state;
      var disabled = await preloadIcon(campaign.toggle.disabledIcon);
      if (root.getAttribute("data-theme-source") === "preference" || state !== optOutState) {
        return false;
      }
      if (state) {
        restoreState(state, false);
      }
      root.setAttribute("data-campaign", campaign.id);
      setThemeSource("campaign-opt-out");
      showToggle(false, disabled);
      state = {
        baseline: baseline,
        campaign: campaign,
        digest: channel.digest,
        disabledIcon: disabled,
        mode: "optout",
        style: null
      };
      return false;
    }

    var previousState = state;
    var next = await prepareCampaign(channel, baseline);
    if (root.getAttribute("data-theme-source") === "preference" || state !== previousState) {
      removeStyle(next);
      return false;
    }
    if (state) {
      restoreState(state, false);
    }
    dispatch("araihu:campaign:before-apply", "apply", campaign.id);
    next.style.media = "all";
    root.setAttribute("data-theme", channel.theme.id);
    root.setAttribute("data-campaign", campaign.id);
    setThemeSource("campaign");
    setBrand({
      logo: campaign.brand.logo.url,
      icon: campaign.brand.icon.url
    });
    showToggle(true, next.enabledIcon);
    state = next;
    dispatch("araihu:campaign:applied", "applied", campaign.id);
    return true;
  }

  function expireCampaign() {
    if (!state) {
      return;
    }
    var campaignID = state.campaign.id;
    restoreState(state, false);
    state = null;
    dispatch("araihu:campaign:restored", "campaign-inactive", campaignID);
  }

  async function refreshOnce() {
    channelURL = resolveChannelURL();
    var channel = await fetchChannel();
    lastChannel = channel;
    if (channel.source === "default") {
      expireCampaign();
      return false;
    }
    return applyCampaign(channel);
  }

  function errorCode(error, fallbackCode) {
    return error instanceof RuntimeError ? error.code : fallbackCode;
  }

  function drainOperations() {
    if (activeOperation || pendingOperations.length === 0) {
      return;
    }
    var entry = pendingOperations.shift();
    activeOperation = entry;
    Promise.resolve().then(entry.operation).catch(function (error) {
      dispatchError(errorCode(error, entry.fallbackCode));
      return false;
    }).then(entry.resolve).finally(function () {
      activeOperation = null;
      drainOperations();
    });
  }

  function enqueueOperation(kind, operation, fallbackCode) {
    var resolveOperation;
    var promise = new Promise(function (resolve) {
      resolveOperation = resolve;
    });
    pendingOperations.push({
      kind: kind,
      operation: operation,
      fallbackCode: fallbackCode,
      promise: promise,
      resolve: resolveOperation
    });
    drainOperations();
    return promise;
  }

  function refresh() {
    return enqueueOperation("refresh", refreshOnce, "refresh-failed");
  }

  async function handleToggle() {
    if (!state || !lastChannel || lastChannel.source !== "campaign") {
      return;
    }
    var campaignID = state.campaign.id;
    if (state.mode === "campaign") {
      var activeState = state;
      var disabled = await preloadIcon(activeState.campaign.toggle.disabledIcon);
      if (state !== activeState || root.getAttribute("data-theme-source") === "preference") {
        return false;
      }
      window.localStorage.setItem(optOutKey(campaignID), "1");
      restoreState(activeState, false);
      root.setAttribute("data-campaign", campaignID);
      setThemeSource("campaign-opt-out");
      showToggle(false, disabled);
      state = {
        baseline: activeState.baseline,
        campaign: activeState.campaign,
        digest: activeState.digest,
        disabledIcon: disabled,
        mode: "optout",
        style: null
      };
      dispatch("araihu:campaign:restored", "campaign-opt-out", campaignID);
      return false;
    }
    window.localStorage.removeItem(optOutKey(campaignID));
    var baseline = state.baseline;
    restoreState(state, false);
    state = null;
    return applyCampaign(lastChannel, baseline);
  }

  function startToggle() {
    return enqueueOperation("toggle", handleToggle, "toggle-failed");
  }

  all("[data-campaign-toggle]").forEach(function (node) {
    node.addEventListener("click", function () {
      startToggle();
    });
  });

  if (window.MutationObserver) {
    new window.MutationObserver(function () {
      var source = root.getAttribute("data-theme-source");
      if (selfThemeSource !== null && source === selfThemeSource) {
        selfThemeSource = null;
        return;
      }
      selfThemeSource = null;
      if (source === "preference" && state) {
        var campaignID = state.campaign.id;
        restoreState(state, true);
        state = null;
        dispatch("araihu:campaign:restored", "preference", campaignID);
      } else if (source === "default") {
        refresh();
      }
    }).observe(root, {
      attributes: true,
      attributeFilter: ["data-theme-source"]
    });
  }

  window.AraiHuCampaign = Object.freeze({
    version: VERSION,
    refresh: refresh
  });

  refresh().catch(function () {
    dispatchError("refresh-failed");
  });
})(window, document);
