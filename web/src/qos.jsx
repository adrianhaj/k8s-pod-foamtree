// QoS vocabulary. The backend passes the pod's status.qosClass through
// untouched; this file decides how each class looks, so the pod boxes, the
// cubes and the sidebar panel agree.

// Ordered by eviction risk, highest first — the order the sidebar lists them.
// Hues are those of the --danger / --warn / --ok tokens, fed through the
// existing pod colour formulas; `sev` picks the matching swatch class.
const QOS_INFO = {
  BestEffort: { hue: 0,   sev: "danger", label: "BestEffort", risk: "no requests or limits — evicted first under memory pressure" },
  Burstable:  { hue: 38,  sev: "warn",   label: "Burstable",  risk: "requests below limits — evicted after BestEffort" },
  Guaranteed: { hue: 160, sev: "ok",     label: "Guaranteed", risk: "requests equal limits — evicted last" },
};

const QOS_ORDER = Object.keys(QOS_INFO);

// Node chrome in QoS mode, and pods whose class the API did not report: only
// the pods should carry meaning, so everything else goes quiet.
const NEUTRAL_HUE = 220;

function qosHue(qos) {
  const cls = QOS_INFO[qos];
  return cls ? cls.hue : NEUTRAL_HUE;
}

window.k8sQos = { QOS_INFO, QOS_ORDER, NEUTRAL_HUE, qosHue };
