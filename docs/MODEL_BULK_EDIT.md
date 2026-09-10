# Model selection and bulk edits

**Models** filters configured models by source or by model/upstream/provider
text. Select individual rows or **Select filtered models**. Selection is by the
source/model pair, so equal model IDs in different sources remain independent.
Selections outside the current filter remain selected and are included in the
visible selection count; **Clear model selection** removes them all.

**Edit selected models** lists every affected pair before editing. Supported
changes are enable state, Auto approval, tier/rating basis, tools and vision.
Every control starts at **Leave unchanged** (`keep` for metadata fields).
Enable and Auto fields also support **Inherit**, preserving the model's nullable
configuration semantics. Setting an unrated tier clears the previous rating
basis; setting a rated tier requires an explicit basis.

Review submits one complete candidate configuration against the captured base
revision. The existing validation/prepare/transaction mechanism applies all
changes together or rejects them without partial publication. For example,
declaring native tools for an adapter that forbids tools rejects the batch.
The preview shows actual field changes. Returning from preview retains the draft;
an external configuration revision change requires refreshing and selecting again.

Unselected models are untouched. Source IDs, upstream model IDs, credentials,
prices, protocols and source/group settings use their existing editors. Saving
resets the page selection and filters with the refreshed configuration.

Enable state and Auto approval are permissions, not tests. Provider, account,
source, group and capability restrictions still apply. Approval may allow paid
traffic when all other routing gates permit it; this is stated in the form before
preview. No generation is sent by selecting or editing models, and no synthetic
rating is promoted to live verification evidence.

Browser regression creates two disabled synthetic sources, selects both models,
checks the required rating basis, and saves their disabled/unapproved state in
one transaction. It then filters and changes one model, verifying the other
retains its previous state. No live service is contacted.
