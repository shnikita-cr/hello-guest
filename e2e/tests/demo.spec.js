const { test, expect } = require('@playwright/test');

const DEMO = process.env.DEMO_URL || 'https://shnikita-cr.github.io/hello-guest/';

test.use({ permissions: ['camera'] });

test('демо: вход, список, камера сканера и отметка по коду', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));

  await page.goto(DEMO);
  await page.getByPlaceholder('PIN').fill('0000');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
  await page.locator('#evList .row', { hasText: 'Демо-мероприятие' }).click();
  await expect(page.locator('#evTitle')).toHaveText('Демо-мероприятие');
  await expect(page.locator('#list .row')).toHaveCount(12);
  await expect(page.locator('#cTotal')).toHaveText('12');

  await page.getByRole('button', { name: 'Сканировать QR/Barcode' }).click();
  await expect(page.locator('#qrPane')).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => !!document.getElementById('qrVideo').srcObject))
    .toBe(true);

  // сердце на сканере должно биться
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.getElementById('liveDot3')).animationName))
    .toBe('beat');

  // потеря сети: полоска появляется на открытой странице
  await page.context().setOffline(true);
  await expect(page.locator('#offlineBar6')).toBeVisible();
  await page.context().setOffline(false);
  await expect(page.locator('#offlineBar6')).toBeHidden({ timeout: 10000 });

  // QR на фейковой камере нет — подаём распознанный код напрямую в обработчик
  await page.evaluate(() => onQR('10002'));
  await expect(page.locator('#qrMsg')).toContainText('Отмечен: Борис Смирнов');
  await expect(page.locator('#qrFlashText')).toHaveText('ОТМЕЧЕН');

  await page.getByRole('button', { name: 'Закрыть' }).click();
  await expect(page.locator('#cArrived')).toHaveText('4');
  await expect(page.locator('#list .row.arrived')).toHaveCount(4);
  expect(errors).toEqual([]);
});
