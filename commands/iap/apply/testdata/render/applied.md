## iap apply: com.example.app

applied to com.example.app (12 change(s)):
  created coins
  migrated legacy_gem (legacy → v2, one-way)
  patched sword (listings, purchaseOptions)
  created offer coins/buy/launch
  patched offer sword/buy/promo (offerTags)
  activated purchase option coins/buy (DRAFT → ACTIVE)
  activated offer coins/buy/launch (DRAFT → ACTIVE)
  deactivated offer sword/buy/promo (ACTIVE → INACTIVE)
  cancelled offer sword/preorder/early (ACTIVE → CANCELLED)
  deactivated purchase option sword/rent (ACTIVE → INACTIVE)
  deleted offer sword/buy/stale
  deleted old
summary: create=1 migrate=1 patch=1 delete=1 offerCreate=1 offerPatch=1 offerDelete=1 state=5 unchanged=1
