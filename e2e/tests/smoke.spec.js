const { test, expect } = require('@playwright/test');

const PIN = process.env.PIN || '';

async function login(page) {
  await page.goto('/');
  await page.getByPlaceholder('PIN').fill(PIN);
  await page.getByRole('button', { name: 'Войти' }).click();
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

test('до входа в баре только Вход и Статистика', async ({ page }) => {
  await expect(page.getByRole('button', { name: 'Вход' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Статистика' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Список' })).toBeHidden();
  await expect(page.getByRole('button', { name: '+ Гость' })).toBeHidden();
  await expect(page.getByRole('button', { name: 'Источники' })).toBeHidden();
});

test('неверный PIN показывает ошибку', async ({ page }) => {
  await page.getByPlaceholder('PIN').fill('000000');
  await page.getByRole('button', { name: 'Войти' }).click();
  await expect(page.getByText('Неверный PIN')).toBeVisible();
});

test('импорт xlsx открывает мероприятие со списком', async ({ page }) => {
  await login(page);
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
  await page.getByRole('button', { name: 'Загрузить выгрузку' }).click();
  await page.locator('#srcEvent').fill('e2e');
  await page.locator('#srcFile').setInputFiles('fixtures/export.xlsx');
  await page.getByRole('button', { name: 'Импортировать файл' }).click();
  await expect(page.locator('#evTitle')).toHaveText('e2e', { timeout: 10000 });
  await expect(page.getByText('Андрей Шепелев')).toBeVisible();
});

test('поиск по имени и по barcode', async ({ page }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  await expect(page.locator('#evTitle')).toHaveText('e2e');
  await page.locator('#q').fill('Шепелев');
  await expect(page.locator('#list .row')).toHaveCount(1);
  await page.locator('#q').fill('1000956039269');
  await expect(page.locator('#list .row')).toHaveCount(1);
  await expect(page.getByText('Андрей Шепелев')).toBeVisible();
  await page.locator('#q').fill('');
  await expect(page.locator('#list .row')).not.toHaveCount(1);
});

test('тап отмечает и снимает отметку', async ({ page }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  const row = page.locator('#list .row', { hasText: 'Андрей Шепелев' });
  await row.click();
  await expect(row).toHaveClass(/arrived/);
  await expect(page.locator('#cArrived')).toHaveText('1');
  page.once('dialog', d => d.accept());
  await row.click();
  await expect(row).not.toHaveClass(/arrived/);
  await expect(page.locator('#cArrived')).toHaveText('0');
});

test('вкладки: + Гость, Статистика, Источники, Список', async ({ page }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  await expect(page.locator('#tabList')).toHaveClass(/on/);

  await page.locator('#tabAdd').click();
  await expect(page.locator('#tabAdd')).toHaveClass(/on/);
  await page.locator('#aName').fill('Иван Тестов');
  await page.getByRole('button', { name: 'Сохранить без отметки' }).click();
  await expect(page.getByText('Иван Тестов')).toBeVisible();
  await expect(page.locator('#tabList')).toHaveClass(/on/);

  await page.locator('#tabStats').click();
  await expect(page.locator('#tabStats')).toHaveClass(/on/);
  await expect(page.locator('#sTotal')).not.toHaveText('0');
  await page.getByRole('button', { name: 'Назад' }).click();
  await expect(page.locator('#tabList')).toHaveClass(/on/);

  await page.locator('#tabSrc').click();
  await expect(page.locator('#tabSrc')).toHaveClass(/on/);
  await page.locator('#tabList').click();
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
});

test('перезагрузка возвращает на тот же экран', async ({ page }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  await expect(page.locator('#evTitle')).toHaveText('e2e');
  await page.reload();
  await expect(page.locator('#evTitle')).toHaveText('e2e');

  await page.locator('#tabList').click();
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
});

test('офлайн: полоска, очередь, возврат онлайн', async ({ page, context }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  await expect(page.locator('#evTitle')).toHaveText('e2e');

  await context.setOffline(true);
  await expect(page.locator('#offlineBar')).toBeVisible({ timeout: 10000 });
  await expect(page.locator('#liveDot')).toHaveClass(/bad/);

  const row = page.locator('#list .row', { hasText: 'Дарья Гусакова' });
  await row.click();
  await expect(row).toHaveClass(/arrived/);
  await expect(page.locator('#offlineBar')).toContainText('1 в очереди');

  await context.setOffline(false);
  await expect(page.locator('#offlineBar')).toBeHidden({ timeout: 10000 });
  await expect(row).toHaveClass(/arrived/);
});

test('сканер на http подсказывает https', async ({ page }) => {
  await login(page);
  await page.locator('.row', { hasText: 'e2e' }).first().click();
  await page.getByRole('button', { name: 'Сканировать QR/Barcode' }).click();
  await expect(page.locator('#qrMsg')).toContainText('https');
});

test('переименование и скрытие мероприятия', async ({ page }) => {
  await login(page);
  await expect(page.getByRole('heading', { name: 'Мероприятия' })).toBeVisible();
  const row = page.locator('#evList .row', { hasText: 'e2e' }).first();

  page.once('dialog', d => d.accept('e2e-renamed'));
  await row.getByRole('button', { name: '✏' }).click();
  await expect(page.locator('#evList', { hasText: 'e2e-renamed' })).toBeVisible();

  const renamed = page.locator('#evList .row', { hasText: 'e2e-renamed' }).first();
  await renamed.getByRole('button', { name: '👁' }).click();
  await expect(page.getByRole('button', { name: 'Показать скрытые (1)' })).toBeVisible();

  await page.getByRole('button', { name: 'Показать скрытые (1)' }).click();
  await page.locator('#evList .row', { hasText: 'e2e-renamed' }).first().getByRole('button', { name: '↩' }).click();
  await expect(page.getByRole('button', { name: 'Показать скрытые (1)' })).toBeHidden();
});
