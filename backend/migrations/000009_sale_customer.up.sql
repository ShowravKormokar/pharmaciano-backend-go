-- ##### FILE: 000009_sale_customer.up.sql ####################################
CREATE TABLE customers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code            VARCHAR(40),
  name            VARCHAR(200) NOT NULL,
  phone           VARCHAR(30),
  email           CITEXT,
  address         VARCHAR(500),
  city            VARCHAR(100),
  date_of_birth   DATE,
  gender          VARCHAR(10) CHECK (gender IS NULL OR gender IN ('male','female','other')),
  blood_group     VARCHAR(4),
  loyalty_points  INTEGER NOT NULL DEFAULT 0,
  credit_limit    NUMERIC(15,4) NOT NULL DEFAULT 0,
  current_due     NUMERIC(15,4) NOT NULL DEFAULT 0,
  notes           TEXT,
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_customers_org_code ON customers(organization_id, code) WHERE deleted_at IS NULL AND code IS NOT NULL;
CREATE INDEX ix_customers_org ON customers(organization_id);
CREATE INDEX ix_customers_name_trgm ON customers USING gin (name gin_trgm_ops);
CREATE TRIGGER tr_customers_upd BEFORE UPDATE ON customers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sales (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  branch_id            UUID NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
  warehouse_id         UUID REFERENCES warehouses(id) ON DELETE SET NULL,
  invoice_no           VARCHAR(40) NOT NULL,
  cashier_id           UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  customer_id          UUID REFERENCES customers(id) ON DELETE SET NULL,
  sale_type            VARCHAR(20) NOT NULL DEFAULT 'retail'
                         CHECK (sale_type IN ('retail','wholesale','online')),
  status               VARCHAR(20) NOT NULL DEFAULT 'completed'
                         CHECK (status IN ('completed','partially_refunded','refunded','cancelled')),
  payment_status       VARCHAR(20) NOT NULL DEFAULT 'paid'
                         CHECK (payment_status IN ('unpaid','partial','paid')),
  subtotal             NUMERIC(15,4) NOT NULL DEFAULT 0,
  discount_amount      NUMERIC(15,4) NOT NULL DEFAULT 0,
  discount_type        VARCHAR(10) CHECK (discount_type IS NULL OR discount_type IN ('percent','flat')),
  tax_amount           NUMERIC(15,4) NOT NULL DEFAULT 0,
  other_charge_amount  NUMERIC(15,4) NOT NULL DEFAULT 0,
  other_charge_type    VARCHAR(10) CHECK (other_charge_type IS NULL OR other_charge_type IN ('percent','flat')),
  coupon_id            UUID,
  coupon_amount        NUMERIC(15,4) NOT NULL DEFAULT 0,
  total_amount         NUMERIC(15,4) NOT NULL DEFAULT 0,
  paid_amount          NUMERIC(15,4) NOT NULL DEFAULT 0,
  change_amount        NUMERIC(15,4) NOT NULL DEFAULT 0,
  prescription_ref     VARCHAR(80),
  notes                TEXT,
  sold_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at           TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_sales_invoice_no ON sales(invoice_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_sales_branch ON sales(branch_id);
CREATE INDEX ix_sales_customer ON sales(customer_id);
CREATE INDEX ix_sales_cashier ON sales(cashier_id);
CREATE INDEX ix_sales_sold_at ON sales(sold_at DESC);
CREATE TRIGGER tr_sales_upd BEFORE UPDATE ON sales
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sale_items (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sale_id          UUID NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  medicine_id      UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  variant_id       UUID REFERENCES medicine_variants(id) ON DELETE SET NULL,
  batch_id         UUID NOT NULL REFERENCES inventory_batches(id) ON DELETE RESTRICT,
  batch_no         VARCHAR(80) NOT NULL,
  pack_type        VARCHAR(20) NOT NULL CHECK (pack_type IN ('unit','strip')),
  quantity         INTEGER NOT NULL,
  unit_price       NUMERIC(15,4) NOT NULL,
  cost_price       NUMERIC(15,4) NOT NULL,
  discount_amount  NUMERIC(15,4) NOT NULL DEFAULT 0,
  tax_amount       NUMERIC(15,4) NOT NULL DEFAULT 0,
  line_total       NUMERIC(15,4) NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
CREATE INDEX ix_sale_items_sale ON sale_items(sale_id);
CREATE INDEX ix_sale_items_batch ON sale_items(batch_id);
CREATE INDEX ix_sale_items_medicine ON sale_items(medicine_id);
CREATE TRIGGER tr_sale_items_upd BEFORE UPDATE ON sale_items
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sale_payments (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sale_id       UUID NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  payment_no    VARCHAR(40) NOT NULL,
  amount        NUMERIC(15,4) NOT NULL,
  method        VARCHAR(20) NOT NULL CHECK (method IN ('cash','card','mobile','bank','mixed')),
  reference_no  VARCHAR(80),
  paid_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_sale_payments_no ON sale_payments(payment_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_sale_payments_sale ON sale_payments(sale_id);
CREATE TRIGGER tr_sale_payments_upd BEFORE UPDATE ON sale_payments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sales_returns (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sale_id        UUID NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  return_no      VARCHAR(40) NOT NULL,
  reason         TEXT NOT NULL,
  refund_amount  NUMERIC(15,4) NOT NULL,
  refund_method  VARCHAR(20) NOT NULL CHECK (refund_method IN ('cash','store_credit','bank')),
  processed_by   UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  processed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  notes          TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_sales_returns_no ON sales_returns(return_no) WHERE deleted_at IS NULL;
CREATE INDEX ix_sales_returns_sale ON sales_returns(sale_id);
CREATE TRIGGER tr_sales_returns_upd BEFORE UPDATE ON sales_returns
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sales_return_items (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  sales_return_id UUID NOT NULL REFERENCES sales_returns(id) ON DELETE CASCADE,
  sale_item_id    UUID NOT NULL REFERENCES sale_items(id) ON DELETE RESTRICT,
  medicine_id     UUID NOT NULL REFERENCES medicines(id) ON DELETE RESTRICT,
  batch_id        UUID NOT NULL REFERENCES inventory_batches(id) ON DELETE RESTRICT,
  batch_no        VARCHAR(80) NOT NULL,
  quantity        INTEGER NOT NULL,
  return_price    NUMERIC(15,4) NOT NULL,
  restockable     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX ix_sales_return_items_return ON sales_return_items(sales_return_id);
CREATE INDEX ix_sales_return_items_batch ON sales_return_items(batch_id);
CREATE TRIGGER tr_sales_return_items_upd BEFORE UPDATE ON sales_return_items
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE coupons (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  code                 VARCHAR(80) NOT NULL,
  name                 VARCHAR(200) NOT NULL,
  description          TEXT,
  discount_type        VARCHAR(20) NOT NULL CHECK (discount_type IN ('percent','flat')),
  discount_value       NUMERIC(15,4) NOT NULL,
  min_purchase_amount  NUMERIC(15,4),
  max_discount_amount  NUMERIC(15,4),
  valid_from           TIMESTAMPTZ NOT NULL,
  valid_to             TIMESTAMPTZ NOT NULL,
  usage_limit          INTEGER,
  usage_count          INTEGER NOT NULL DEFAULT 0,
  per_customer_limit   INTEGER,
  is_active            BOOLEAN NOT NULL DEFAULT TRUE,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at           TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_coupons_org_code ON coupons(organization_id, code) WHERE deleted_at IS NULL;
CREATE INDEX ix_coupons_valid ON coupons(valid_from, valid_to) WHERE is_active AND deleted_at IS NULL;
CREATE TRIGGER tr_coupons_upd BEFORE UPDATE ON coupons
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE offers (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name            VARCHAR(200) NOT NULL,
  offer_type      VARCHAR(20) NOT NULL CHECK (offer_type IN ('bogo','percent_off','flat_off','bundle')),
  config          JSONB NOT NULL DEFAULT '{}',
  valid_from      TIMESTAMPTZ NOT NULL,
  valid_to        TIMESTAMPTZ NOT NULL,
  is_active       BOOLEAN NOT NULL DEFAULT TRUE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX ix_offers_org ON offers(organization_id);
CREATE INDEX ix_offers_valid ON offers(valid_from, valid_to) WHERE is_active AND deleted_at IS NULL;
CREATE TRIGGER tr_offers_upd BEFORE UPDATE ON offers
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE coupon_redemptions (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  coupon_id        UUID NOT NULL REFERENCES coupons(id) ON DELETE CASCADE,
  sale_id          UUID NOT NULL REFERENCES sales(id) ON DELETE CASCADE,
  customer_id      UUID REFERENCES customers(id) ON DELETE SET NULL,
  discount_amount  NUMERIC(15,4) NOT NULL,
  redeemed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX ux_coupon_redemptions_coupon_sale ON coupon_redemptions(coupon_id, sale_id);
CREATE INDEX ix_coupon_redemptions_sale ON coupon_redemptions(sale_id);
CREATE TRIGGER tr_coupon_redemptions_upd BEFORE UPDATE ON coupon_redemptions
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();