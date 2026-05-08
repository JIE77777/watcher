const canvas = document.querySelector("#game");
const ctx = canvas.getContext("2d");
const scoreEl = document.querySelector("#score");
const levelEl = document.querySelector("#level");
const ballsEl = document.querySelector("#balls");
const statusEl = document.querySelector("#status");
const startButton = document.querySelector("#start-button");
const resetButton = document.querySelector("#reset-button");

const width = canvas.width;
const height = canvas.height;
const brickGap = 6;
const brickWidth = 50;
const brickHeight = 22;
const brickStartX = 105;
const brickStartY = 54;
const paddleBaseWidth = 120;
const paddleHeight = 16;
const ballRadius = 7;
const powerDuration = 10000;
const explosionRadius = 72;

const levels = [
  {
    name: "方阵",
    map: [
      "NNNNSSNNNNNN",
      "N22NNNN22NEN",
      "NNN3333NNNNN",
      "E22NNSSNN22E",
      "NNNNNNNNNNNN",
    ],
  },
  {
    name: "箭头",
    map: [
      ".....EE.....",
      "....N33N....",
      "...N3333N...",
      "..S222222S..",
      ".NNNNNNNNNN.",
      "....N22N....",
    ],
  },
  {
    name: "城墙",
    map: [
      "SNNENNNNENNS",
      "N3333333333N",
      "N2..S..S..2N",
      "N2222222222N",
      "E..N3333N..E",
      "NNNNNNNNNNNN",
    ],
  },
];

let paddle;
let balls;
let bricks;
let powerups;
let bullets;
let particles;
let floatTexts;
let score;
let lives;
let levelIndex;
let running;
let gameOver;
let finished;
let animationId;
let lastTime;
let keys;

function resetGame() {
  score = 0;
  lives = 3;
  levelIndex = 0;
  gameOver = false;
  finished = false;
  keys = new Set();
  loadLevel(levelIndex);
  setStatus("按空格开始，鼠标或方向键移动弹板。");
}

function loadLevel(index) {
  paddle = {
    x: width / 2 - paddleBaseWidth / 2,
    y: height - 58,
    width: paddleBaseWidth,
    speed: 600,
    extendedUntil: 0,
    shooterUntil: 0,
    shootCooldownUntil: 0,
  };
  balls = [createBall(width / 2, paddle.y - ballRadius - 2, -220, -330)];
  bricks = createBricks(levels[index].map);
  powerups = [];
  bullets = [];
  particles = [];
  floatTexts = [];
  running = false;
  lastTime = 0;
  cancelAnimationFrame(animationId);
  updateHud();
  draw(0);
}

function createBall(x, y, vx, vy, explosiveUntil = 0) {
  return { x, y, vx, vy, radius: ballRadius, explosiveUntil };
}

function createBricks(map) {
  const rowWidth = map[0].length * brickWidth + (map[0].length - 1) * brickGap;
  const offsetX = brickStartX + (width - brickStartX * 2 - rowWidth) / 2;
  const layout = [];

  map.forEach((row, rowIndex) => {
    [...row].forEach((cell, colIndex) => {
      if (cell === ".") {
        return;
      }

      const type = cell === "S" ? "special" : cell === "E" ? "explosive" : "normal";
      const maxHp = cell === "2" ? 2 : cell === "3" ? 3 : 1;

      layout.push({
        x: offsetX + colIndex * (brickWidth + brickGap),
        y: brickStartY + rowIndex * (brickHeight + brickGap),
        width: brickWidth,
        height: brickHeight,
        hp: type === "explosive" ? 1 : maxHp,
        maxHp: type === "explosive" ? 1 : maxHp,
        type,
        alive: true,
      });
    });
  });

  return layout;
}

function toggleRunning() {
  if (gameOver || finished) {
    resetGame();
  }

  running = !running;
  setStatus(running ? `第 ${levelIndex + 1} 关：${levels[levelIndex].name}` : "已暂停。");

  if (running) {
    lastTime = performance.now();
    animationId = requestAnimationFrame(loop);
  } else {
    cancelAnimationFrame(animationId);
    draw(performance.now());
  }
}

function loop(time) {
  const delta = Math.min((time - lastTime) / 1000, 0.025);
  lastTime = time;
  update(delta, time);
  draw(time);

  if (running) {
    animationId = requestAnimationFrame(loop);
  }
}

function update(delta, time) {
  updatePaddle(delta, time);
  updateBalls(delta, time);
  updatePowerups(delta);
  updateBullets(delta, time);
  updateParticles(delta);
  updateFloatTexts(delta);
  checkWin();
  updateHud();
}

function updatePaddle(delta, time) {
  paddle.width = time < paddle.extendedUntil ? 176 : paddleBaseWidth;

  if (keys.has("ArrowLeft") || keys.has("KeyA")) {
    paddle.x -= paddle.speed * delta;
  }

  if (keys.has("ArrowRight") || keys.has("KeyD")) {
    paddle.x += paddle.speed * delta;
  }

  paddle.x = clamp(paddle.x, 0, width - paddle.width);
}

function updateBalls(delta, time) {
  for (const ball of balls) {
    ball.x += ball.vx * delta;
    ball.y += ball.vy * delta;

    if (ball.x - ball.radius < 0 || ball.x + ball.radius > width) {
      ball.vx *= -1;
      ball.x = clamp(ball.x, ball.radius, width - ball.radius);
    }

    if (ball.y - ball.radius < 0) {
      ball.vy = Math.abs(ball.vy);
      ball.y = ball.radius;
    }

    collidePaddle(ball);
    collideBricks(ball, time);
  }

  balls = balls.filter((ball) => ball.y - ball.radius <= height);

  if (balls.length === 0) {
    loseLife();
  }
}

function collidePaddle(ball) {
  const withinX = ball.x > paddle.x && ball.x < paddle.x + paddle.width;
  const touchingY =
    ball.y + ball.radius >= paddle.y && ball.y - ball.radius <= paddle.y + paddleHeight;

  if (withinX && touchingY && ball.vy > 0) {
    const hit = (ball.x - (paddle.x + paddle.width / 2)) / (paddle.width / 2);
    const speed = Math.hypot(ball.vx, ball.vy) + 8;
    ball.vx = hit * 350;
    ball.vy = -Math.sqrt(Math.max(speed * speed - ball.vx * ball.vx, 170 * 170));
    ball.y = paddle.y - ball.radius - 1;
  }
}

function collideBricks(ball, time) {
  for (const brick of bricks) {
    if (!brick.alive || !circleRectCollision(ball, brick)) {
      continue;
    }

    const explosiveBall = time < ball.explosiveUntil;
    damageBrick(brick, 1, time);

    if (explosiveBall) {
      explodeAt(centerX(brick), centerY(brick), time, brick);
    }

    bounceBallFromBrick(ball, brick);
    return;
  }
}

function damageBrick(brick, damage, time, fromExplosion = false) {
  if (!brick.alive) {
    return;
  }

  brick.hp -= damage;
  spawnParticles(centerX(brick), centerY(brick), brickColor(brick), 8);

  if (brick.hp > 0) {
    return;
  }

  brick.alive = false;
  score += brick.type === "normal" ? 10 : 18;

  if (brick.type === "special" && !fromExplosion) {
    spawnPowerup(centerX(brick), centerY(brick));
  }

  if (brick.type === "explosive") {
    explodeAt(centerX(brick), centerY(brick), time, brick);
  }
}

function explodeAt(x, y, time, source) {
  spawnParticles(x, y, "#fb923c", 34);
  floatTexts.push(createFloatText("爆炸！", x, y - 12, "#fed7aa"));

  for (const brick of bricks) {
    if (!brick.alive || brick === source) {
      continue;
    }

    const dx = centerX(brick) - x;
    const dy = centerY(brick) - y;

    if (Math.hypot(dx, dy) <= explosionRadius) {
      damageBrick(brick, 1, time, true);
    }
  }
}

function bounceBallFromBrick(ball, brick) {
  const overlapLeft = ball.x + ball.radius - brick.x;
  const overlapRight = brick.x + brick.width - (ball.x - ball.radius);
  const overlapTop = ball.y + ball.radius - brick.y;
  const overlapBottom = brick.y + brick.height - (ball.y - ball.radius);
  const minOverlap = Math.min(overlapLeft, overlapRight, overlapTop, overlapBottom);

  if (minOverlap === overlapLeft || minOverlap === overlapRight) {
    ball.vx *= -1;
  } else {
    ball.vy *= -1;
  }
}

function updatePowerups(delta) {
  for (const powerup of powerups) {
    powerup.y += powerup.vy * delta;

    const hitPaddle =
      powerup.x > paddle.x &&
      powerup.x < paddle.x + paddle.width &&
      powerup.y + powerup.size >= paddle.y &&
      powerup.y <= paddle.y + paddleHeight;

    if (hitPaddle) {
      applyPowerup(powerup.type, powerup.x, powerup.y);
      powerup.collected = true;
    }
  }

  powerups = powerups.filter((powerup) => !powerup.collected && powerup.y < height + 30);
}

function updateBullets(delta, time) {
  for (const bullet of bullets) {
    bullet.y -= bullet.speed * delta;

    for (const brick of bricks) {
      if (!brick.alive || !rectsOverlap(bullet, brick)) {
        continue;
      }

      damageBrick(brick, 1, time);
      bullet.used = true;
      break;
    }
  }

  bullets = bullets.filter((bullet) => !bullet.used && bullet.y + bullet.height > 0);
}

function updateParticles(delta) {
  for (const particle of particles) {
    particle.x += particle.vx * delta;
    particle.y += particle.vy * delta;
    particle.life -= delta;
  }

  particles = particles.filter((particle) => particle.life > 0);
}

function updateFloatTexts(delta) {
  for (const text of floatTexts) {
    text.y -= 38 * delta;
    text.life -= delta;
  }

  floatTexts = floatTexts.filter((text) => text.life > 0);
}

function applyPowerup(type, x, y) {
  const now = performance.now();

  if (type === "wide") {
    paddle.extendedUntil = now + powerDuration;
    showBuffText("弹板变长！", x, y, "#bbf7d0");
    setStatus("获得装备：弹板变长。");
  }

  if (type === "multi") {
    const base = balls[0] ?? createBall(width / 2, paddle.y - ballRadius - 2, -220, -330);
    balls.push(
      createBall(base.x, base.y, -280, -335, base.explosiveUntil),
      createBall(base.x, base.y, 280, -335, base.explosiveUntil),
    );
    showBuffText("球数增加！", x, y, "#bae6fd");
    setStatus("获得装备：球数增加。");
  }

  if (type === "blast") {
    for (const ball of balls) {
      ball.explosiveUntil = now + powerDuration;
    }
    showBuffText("爆炸球！", x, y, "#fed7aa");
    setStatus("获得装备：爆炸球，限时范围伤害。");
  }

  if (type === "shooter") {
    paddle.shooterUntil = now + powerDuration;
    showBuffText("火力开启！", x, y, "#fef08a");
    setStatus("获得装备：弹板射击，单击鼠标发射。");
  }
}

function spawnPowerup(x, y) {
  const types = ["wide", "multi", "blast", "shooter"];
  const type = types[Math.floor(Math.random() * types.length)];
  powerups.push({ x, y, type, vy: 115, size: 17, collected: false });
}

function showBuffText(text, x, y, color) {
  floatTexts.push(createFloatText(text, x, y - 18, color));
}

function createFloatText(text, x, y, color) {
  return { text, x, y, color, life: 1.2, maxLife: 1.2 };
}

function shoot() {
  const now = performance.now();

  if (now > paddle.shooterUntil || now < paddle.shootCooldownUntil || !running) {
    return;
  }

  paddle.shootCooldownUntil = now + 180;
  bullets.push(
    { x: paddle.x + 12, y: paddle.y - 12, width: 5, height: 16, speed: 540 },
    { x: paddle.x + paddle.width - 17, y: paddle.y - 12, width: 5, height: 16, speed: 540 },
  );
}

function loseLife() {
  lives -= 1;

  if (lives <= 0) {
    running = false;
    gameOver = true;
    setStatus(`游戏结束，最终分数：${score}。`);
    return;
  }

  balls = [createBall(width / 2, paddle.y - ballRadius - 2, -220, -330)];
  setStatus("损失 1 命。");
}

function checkWin() {
  const remaining = bricks.some((brick) => brick.alive);

  if (remaining) {
    return;
  }

  if (levelIndex < levels.length - 1) {
    levelIndex += 1;
    setStatus(`进入第 ${levelIndex + 1} 关：${levels[levelIndex].name}`);
    loadLevel(levelIndex);
    return;
  }

  running = false;
  finished = true;
  setStatus(`全部关卡通关，最终分数：${score}。`);
}

function draw(time) {
  drawBackground();
  drawLives();
  drawBricks();
  drawPowerups();
  drawBullets();
  drawPaddle(time);
  drawBalls(time);
  drawParticles();
  drawFloatTexts();

  if (gameOver || finished || !running) {
    drawOverlay();
  }
}

function drawBackground() {
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#101827";
  ctx.fillRect(0, 0, width, height);

  ctx.strokeStyle = "rgba(255, 255, 255, 0.06)";
  ctx.lineWidth = 1;
  for (let x = 32; x < width; x += 32) {
    ctx.beginPath();
    ctx.moveTo(x, 0);
    ctx.lineTo(x, height);
    ctx.stroke();
  }
}

function drawLives() {
  for (let i = 0; i < lives; i += 1) {
    const x = 24 + i * 24;
    const y = 24;
    ctx.fillStyle = "#fb7185";
    ctx.beginPath();
    ctx.arc(x - 5, y, 7, Math.PI, 0);
    ctx.arc(x + 5, y, 7, Math.PI, 0);
    ctx.lineTo(x, y + 14);
    ctx.closePath();
    ctx.fill();
  }
}

function drawBricks() {
  for (const brick of bricks) {
    if (!brick.alive) {
      continue;
    }

    ctx.fillStyle = brickColor(brick);
    roundRect(brick.x, brick.y, brick.width, brick.height, 4);
    ctx.fill();
    ctx.fillStyle = "rgba(255, 255, 255, 0.24)";
    ctx.fillRect(brick.x + 3, brick.y + 3, brick.width - 6, 4);

    if (brick.maxHp > 1) {
      ctx.fillStyle = "rgba(15, 23, 42, 0.45)";
      ctx.fillRect(brick.x + 6, brick.y + brick.height - 6, brick.width - 12, 3);
      ctx.fillStyle = "#ffffff";
      ctx.fillRect(
        brick.x + 6,
        brick.y + brick.height - 6,
        ((brick.width - 12) * brick.hp) / brick.maxHp,
        3,
      );
    }

    if (brick.type === "special") {
      ctx.fillStyle = "#ffffff";
      ctx.font = "700 14px system-ui, sans-serif";
      ctx.textAlign = "center";
      ctx.fillText("?", centerX(brick), brick.y + 16);
    }

    if (brick.type === "explosive") {
      ctx.fillStyle = "#ffffff";
      ctx.beginPath();
      ctx.arc(centerX(brick), centerY(brick), 4, 0, Math.PI * 2);
      ctx.fill();
    }
  }
}

function drawPaddle(time) {
  ctx.fillStyle = time < paddle.shooterUntil ? "#38bdf8" : "#e2e8f0";
  roundRect(paddle.x, paddle.y, paddle.width, paddleHeight, 8);
  ctx.fill();

  if (time < paddle.shooterUntil) {
    ctx.fillStyle = "#f8fafc";
    ctx.fillRect(paddle.x + 8, paddle.y - 6, 10, 8);
    ctx.fillRect(paddle.x + paddle.width - 18, paddle.y - 6, 10, 8);
  }
}

function drawBalls(time) {
  for (const ball of balls) {
    const explosive = time < ball.explosiveUntil;
    ctx.fillStyle = explosive ? "#fb923c" : "#f8fafc";
    ctx.beginPath();
    ctx.arc(ball.x, ball.y, ball.radius, 0, Math.PI * 2);
    ctx.fill();

    if (explosive) {
      ctx.strokeStyle = "rgba(251, 146, 60, 0.45)";
      ctx.lineWidth = 5;
      ctx.beginPath();
      ctx.arc(ball.x, ball.y, ball.radius + 4, 0, Math.PI * 2);
      ctx.stroke();
    }
  }
}

function drawPowerups() {
  const labels = {
    wide: "W",
    multi: "+2",
    blast: "B",
    shooter: "S",
  };

  for (const powerup of powerups) {
    ctx.fillStyle = powerupColor(powerup.type);
    ctx.beginPath();
    ctx.arc(powerup.x, powerup.y, powerup.size, 0, Math.PI * 2);
    ctx.fill();
    ctx.fillStyle = "#ffffff";
    ctx.font = "700 13px system-ui, sans-serif";
    ctx.textAlign = "center";
    ctx.fillText(labels[powerup.type], powerup.x, powerup.y + 5);
  }
}

function drawBullets() {
  ctx.fillStyle = "#fde047";
  for (const bullet of bullets) {
    ctx.fillRect(bullet.x, bullet.y, bullet.width, bullet.height);
  }
}

function drawParticles() {
  for (const particle of particles) {
    ctx.globalAlpha = Math.max(particle.life / particle.maxLife, 0);
    ctx.fillStyle = particle.color;
    ctx.fillRect(particle.x, particle.y, particle.size, particle.size);
  }
  ctx.globalAlpha = 1;
}

function drawFloatTexts() {
  ctx.textAlign = "center";
  ctx.font = "700 24px system-ui, sans-serif";

  for (const item of floatTexts) {
    ctx.globalAlpha = Math.max(item.life / item.maxLife, 0);
    ctx.fillStyle = item.color;
    ctx.fillText(item.text, item.x, item.y);
  }

  ctx.globalAlpha = 1;
}

function drawOverlay() {
  let text = "暂停中";

  if (gameOver) {
    text = "Game Over";
  }

  if (finished) {
    text = "全部通关";
  }

  ctx.fillStyle = "rgba(15, 23, 42, 0.52)";
  ctx.fillRect(0, 0, width, height);
  ctx.fillStyle = "#ffffff";
  ctx.font = "700 42px system-ui, sans-serif";
  ctx.textAlign = "center";
  ctx.fillText(text, width / 2, height / 2);
}

function spawnParticles(x, y, color, count) {
  for (let i = 0; i < count; i += 1) {
    particles.push({
      x,
      y,
      vx: Math.cos(i + Math.random()) * (70 + Math.random() * 130),
      vy: Math.sin(i * 1.7 + Math.random()) * (70 + Math.random() * 130),
      size: 3 + Math.random() * 4,
      life: 0.35 + Math.random() * 0.4,
      maxLife: 0.75,
      color,
    });
  }
}

function updateHud() {
  scoreEl.textContent = score;
  levelEl.textContent = levelIndex + 1;
  ballsEl.textContent = balls.length;
}

function setStatus(message) {
  statusEl.textContent = message;
}

function brickColor(brick) {
  if (brick.type === "special") {
    return "#8b5cf6";
  }

  if (brick.type === "explosive") {
    return "#f97316";
  }

  if (brick.maxHp === 3) {
    return "#ef4444";
  }

  if (brick.maxHp === 2) {
    return "#0ea5e9";
  }

  return "#22c55e";
}

function powerupColor(type) {
  return {
    wide: "#22c55e",
    multi: "#0ea5e9",
    blast: "#f97316",
    shooter: "#eab308",
  }[type];
}

function circleRectCollision(circle, rect) {
  const closestX = clamp(circle.x, rect.x, rect.x + rect.width);
  const closestY = clamp(circle.y, rect.y, rect.y + rect.height);
  const dx = circle.x - closestX;
  const dy = circle.y - closestY;

  return dx * dx + dy * dy <= circle.radius * circle.radius;
}

function rectsOverlap(a, b) {
  return (
    a.x < b.x + b.width &&
    a.x + a.width > b.x &&
    a.y < b.y + b.height &&
    a.y + a.height > b.y
  );
}

function centerX(rect) {
  return rect.x + rect.width / 2;
}

function centerY(rect) {
  return rect.y + rect.height / 2;
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function roundRect(x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

document.addEventListener("keydown", (event) => {
  if (event.code === "Space") {
    event.preventDefault();
    toggleRunning();
    return;
  }

  keys.add(event.code);
});

document.addEventListener("keyup", (event) => {
  keys.delete(event.code);
});

canvas.addEventListener("mousemove", (event) => {
  const rect = canvas.getBoundingClientRect();
  const scaleX = width / rect.width;
  paddle.x = clamp((event.clientX - rect.left) * scaleX - paddle.width / 2, 0, width - paddle.width);
});

canvas.addEventListener("click", shoot);
startButton.addEventListener("click", toggleRunning);
resetButton.addEventListener("click", resetGame);

resetGame();
