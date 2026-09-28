## iap apply: com.example.app

plan for com.example.app (12 change(s)):
  create coins
  migrate legacy_gem (legacy → v2, one-way)
  patch sword (listings, purchaseOptions)
  create offer coins/buy/launch
  patch offer sword/buy/promo (offerTags)
  activate purchase option coins/buy (DRAFT → ACTIVE)
  activate offer coins/buy/launch (DRAFT → ACTIVE)
  deactivate offer sword/buy/promo (ACTIVE → INACTIVE)
  cancel offer sword/preorder/early (ACTIVE → CANCELLED)
  deactivate purchase option sword/rent (ACTIVE → INACTIVE)
  delete offer sword/buy/stale
  delete old
summary: create=1 migrate=1 patch=1 delete=1 offerCreate=1 offerPatch=1 offerDelete=1 state=5 unchanged=1
requires: confirm, migrate
